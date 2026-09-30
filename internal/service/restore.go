package service

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"app/internal/auth"
	"app/internal/dbbackup"
	"app/internal/dbfile"
	"app/internal/paths"
	"app/internal/repository"
	"app/internal/usecase"
)

// safetyCopyKeep 是「破坏性操作前自动副本」保留的份数。
// 备份目录会随每次恢复/清空增长，必须有上限。
const safetyCopyKeep = 5

// safetyCopyPrefixes 各类破坏性操作副本的文件名前缀。
// 与用户手动「一键备份」的 backup_ 区分，便于识别与单独清理。
const (
	prefixPreRestore = "pre_restore"
	prefixPreClear   = "pre_clear"
)

// 统计类型与用例层共享（D-013 可移植性：传输/用例类型不锁在业务包）。
type RestoreResult = usecase.RestoreResult
type ClearResult = usecase.ClearResult

// restoreRowsSummary 返回按表名排序的可读摘要，例如 "parts=210, products=70"。
func restoreRowsSummary(counts map[string]int) string {
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", k, counts[k]))
	}
	return strings.Join(parts, ", ")
}

// RestoreDatabase 将整个数据库恢复到 filePath 记录的时刻。
//
// MySQL: 只回灌业务表数据，忽略 DDL / 会话语句；users、schema_migrations、
// db_identity 保留现场状态。「清空 + 回灌」在单事务中完成。回滚成功才可
// 断言未应用；COMMIT 结果不明与提交后失败必须分别报告。
// SQLite: D-014 整文件替换，账号、表结构、数据库身份随快照一起回退。
// 两种后端操作前都生成可从备份目录选择的完整副本，副本失败即中止。
func (s *Service) RestoreDatabase(filePath string) (RestoreResult, error) {
	var res RestoreResult

	// 备份类型必须与当前存储后端一致。类型按文件内容判断（不是扩展名），
	// 与 UI 的确认文案 / 是否需要重启保持同一口径。
	sqliteBackend := s.snapshots != nil && s.snapshots.Kind() == usecase.SnapshotKindSQLite
	if dbfile.IsSQLiteSnapshot(filePath) {
		if !sqliteBackend {
			return res, fmt.Errorf("当前使用 MySQL 存储，无法应用 SQLite 整库快照（.db）；请导入 .sql 备份")
		}
		// D-014: full snapshot rollback via file replace (no merge). Keep a
		// selectable backup in the backup directory before closing DB handles.
		pre, err := s.safetyCopy(prefixPreRestore)
		if err != nil {
			return res, fmt.Errorf("恢复前备份失败，已中止恢复：%w", err)
		}
		res.PreRestore = pre
		// Restore also keeps its own sidecar file for low-level replacement
		// recovery; the backup-directory copy is the UI's rollback path.
		_, rerr := s.snapshots.Restore(filePath)
		if rerr != nil {
			// File replacement may have started; never promise the old database
			// is intact without inspecting the on-disk state.
			return res, &usecase.OperationError{State: usecase.OperationUnknown, Err: rerr}
		}
		res.Statements = 1
		return res, nil
	}
	if sqliteBackend {
		return res, fmt.Errorf("当前使用 SQLite 存储，不支持导入 MySQL 的 .sql 备份；请导入 .db 整库快照")
	}

	// 闸门：必须是本应用导出的完整备份。缺工具完成标记说明文件被截断，
	// 拿它覆盖数据只会得到一个残缺的库。
	if err := dbbackup.VerifyDumpFile(filePath); err != nil {
		return res, fmt.Errorf("备份文件校验未通过，已中止恢复：%w", err)
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return res, fmt.Errorf("read file: %w", err)
	}
	content := strings.ReplaceAll(string(data), "\r\n", "\n")

	// 恢复前副本。失败即中止：宁可不做，也不能在没有兜底的情况下覆盖数据。
	pre, err := s.safetyCopy(prefixPreRestore)
	if err != nil {
		return res, fmt.Errorf("恢复前备份失败，已中止恢复：%w", err)
	}
	res.PreRestore = pre

	stmts, err := func() (int, error) {
		var n int
		if err := s.repo.WithBulkLoad(func(tx repository.TxOps) error {
			// 先清空业务表：这是「覆盖」而非「追加」的关键。
			if err := deleteBusinessTables(tx); err != nil {
				return fmt.Errorf("restore clear failed: %w", err)
			}
			count, err := replayDumpInserts(tx, content)
			n = count
			return err
		}); err != nil {
			return n, err
		}
		return n, nil
	}()
	if err != nil {
		// Only a confirmed rollback means the data was not changed. A failed
		// COMMIT is uncertain; cleanup after a successful COMMIT is already applied.
		return RestoreResult{PreRestore: pre}, bulkLoadOutcome(err)
	}
	res.Statements = stmts

	counts, err := s.countBusinessTables()
	if err != nil {
		return res, &usecase.OperationError{State: usecase.OperationApplied, Err: err}
	}
	res.TableCounts = counts

	// 审计留痕放在恢复之后：audit_log 已被清空并由备份重建，
	// 只有提交后再写，这一步才会留存在恢复后的库里。
	if err := s.writeAudit(auditEntry{
		TableName: "database",
		Action:    "RESTORE",
		NewData: map[string]any{
			"file":          filePath,
			"pre_restore":   pre,
			"statements":    stmts,
			"table_counts":  counts,
			"preserved":     preservedTables,
			"schema_change": false,
		},
		Operator: auth.OperatorName(),
	}); err != nil {
		return res, &usecase.OperationError{State: usecase.OperationApplied, Err: err}
	}
	return res, nil
}

// replayDumpInserts 执行 dump 中的 INSERT 语句，跳过系统表。
// 返回成功执行的语句数。
//
// 解析规则与旧实现一致：逐行扫描，只认 INSERT INTO；跨行语句用缓冲拼回。
// 新增：识别目标表名，对 preservedTables 里的表直接跳过——
// 这些表描述的是现场状态，不该被备份里的值覆盖。
func replayDumpInserts(tx repository.TxOps, content string) (int, error) {
	lines := strings.Split(content, "\n")
	var buf strings.Builder
	inInsert := false
	statements := 0

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "--") || strings.HasPrefix(trimmed, "/*") {
			continue
		}
		if !inInsert {
			if !strings.HasPrefix(strings.ToUpper(trimmed), "INSERT INTO") {
				continue
			}
			if insertTargetIsQualified(trimmed) {
				// A dump from this app never qualifies table names. Reject a
				// modified dump rather than writing to system or other schemas.
				return statements, fmt.Errorf("restore SQL contains qualified INSERT target")
			}
			if insertTargetIsPreserved(trimmed) {
				// 跳过整条语句：多行 INSERT 需要把缓冲状态保持为「不在语句中」。
				continue
			}
			buf.Reset()
			buf.WriteString(line)
			if strings.HasSuffix(strings.TrimRight(trimmed, " \t"), ";") {
				if err := execRestoreStatement(tx, buf.String(), &statements); err != nil {
					return statements, err
				}
			} else {
				inInsert = true
			}
			continue
		}
		buf.WriteString("\n")
		buf.WriteString(line)
		if strings.HasSuffix(strings.TrimRight(trimmed, " \t"), ";") {
			if err := execRestoreStatement(tx, buf.String(), &statements); err != nil {
				return statements, err
			}
			inInsert = false
		}
	}
	if inInsert {
		return statements, fmt.Errorf("restore SQL contains unterminated INSERT")
	}
	return statements, nil
}

func execRestoreStatement(tx repository.TxOps, stmt string, statements *int) error {
	if _, err := tx.Exec(stmt); err != nil {
		return fmt.Errorf("restore INSERT failed: %w", err)
	}
	*statements++
	return nil
}

// insertTargetIsQualified detects db.table and `db`.`table` targets without
// treating a period inside a single quoted identifier as a schema separator.
func insertTargetIsQualified(stmt string) bool {
	rest := strings.TrimSpace(stmt[len("INSERT INTO"):])
	if rest == "" {
		return false
	}
	if rest[0] == '`' || rest[0] == '"' {
		if end := strings.IndexByte(rest[1:], rest[0]); end >= 0 {
			return strings.HasPrefix(strings.TrimSpace(rest[end+2:]), ".")
		}
		return false
	}
	end := strings.IndexAny(rest, " \t(")
	if end < 0 {
		return strings.Contains(rest, ".")
	}
	return strings.Contains(rest[:end], ".") || strings.HasPrefix(strings.TrimSpace(rest[end:]), ".")
}

// insertTargetIsPreserved 判断一条 INSERT 是否写入必须保留的系统表。
// 兼容备份工具的反引号写法：INSERT INTO `schema_migrations` VALUES ...
// 也兼容未加引号的写法：INSERT INTO schema_migrations (version) VALUES (1)
func insertTargetIsPreserved(stmt string) bool {
	rest := strings.TrimSpace(stmt[len("INSERT INTO"):])
	if rest == "" {
		return false
	}
	// 表名被反引号或双引号整体包裹时，取到配对的结束引号为止。
	if rest[0] == '`' || rest[0] == '"' {
		quote := rest[0]
		if end := strings.IndexByte(rest[1:], quote); end >= 0 {
			return isPreservedTable(rest[1 : 1+end])
		}
		return false
	}
	// 未加引号：取到第一个空白或左括号为止。
	if end := strings.IndexAny(rest, " \t("); end >= 0 {
		return isPreservedTable(rest[:end])
	}
	return isPreservedTable(rest)
}

// safetyCopy 在破坏性操作前生成一份完整副本，并按份数上限清理旧副本。
// 任一环节失败都返回错误，调用方必须中止操作。
func (s *Service) safetyCopy(prefix string) (string, error) {
	port, ok := s.snapshots.(usecase.PrefixSnapshotPort)
	if !ok {
		return "", fmt.Errorf("当前存储后端不支持生成操作前副本")
	}
	path, err := port.PrefixSnapshot(paths.BackupDir(), prefix)
	if err != nil {
		return "", err
	}
	pruneSafetyCopies(prefix)
	return path, nil
}

// pruneSafetyCopies 按份数上限清理某类副本。失败不影响本次操作结果。
func pruneSafetyCopies(prefix string) {
	switch prefix {
	case prefixPreRestore, prefixPreClear:
		dbbackup.PruneSnapshots(paths.BackupDir(), prefix, safetyCopyKeep)
	}
}

// countBusinessTables 读取恢复后各业务表的行数，供用户核对。
func (s *Service) countBusinessTables() (map[string]int, error) {
	counts := make(map[string]int, len(businessTables))
	err := s.repo.WithTx(func(tx repository.TxOps) error {
		for _, table := range businessTables {
			var n int
			if err := tx.CountRows(table, &n); err != nil {
				return fmt.Errorf("count %s: %w", table, err)
			}
			counts[table] = n
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return counts, nil
}
