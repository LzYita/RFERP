package migrate

import (
	"database/sql"
	"fmt"
	"log"

	"github.com/jmoiron/sqlx"
)

// SQLiteOptions 描述 SQLite 迁移所需的库外信息（文件路径与快照实现）。
type SQLiteOptions struct {
	// DBPath 是 SQLite 数据库文件路径；为空则无法在升级前做快照。
	DBPath string
	// SnapshotDir 是升级前快照的保存目录。
	SnapshotDir string
	// Snapshot 是快照实现（VACUUM INTO + 校验），由调用方注入，避免 migrate
	// 依赖仓储层；签名与 repository.SnapshotSQLiteFile 一致。
	Snapshot func(dbPath, saveDir string) (string, error)
}

// RunSQLite 将 SQLite 库迁到目标版本。
//
// 策略（D-015）：
//   - 空库：应用 v11 终态基线，并记 schema_migrations = 1..11
//   - 低于 v11 且已有业务表：不支持从历史半成品升级（Local 新库路径专用），明确报错
//   - 高于 11 的 pending：按 steps 双端描述执行
//
// 备份策略：空库基线不需要升级前快照；一旦有 pending 步骤要作用在**已有数据的库**
// 上，本函数会先用 opts.Snapshot 生成并校验快照，**快照失败则中止迁移**。
func RunSQLite(db *sqlx.DB, opts SQLiteOptions) (Result, error) {
	var res Result
	if err := execAll(db, []string{sqliteSchemaMigrationsDDL}); err != nil {
		return res, err
	}
	cur, err := currentVersionSQL(db)
	if err != nil {
		return res, err
	}
	res.FromVersion = cur

	if cur == 0 {
		empty, err := sqliteDatabaseEmpty(db)
		if err != nil {
			return res, err
		}
		if empty {
			// 全新空库：直接落基线，不需要快照。
			log.Printf("migrate/sqlite: applying baseline v%d", CurrentSchemaVersion)
			if err := applySQLiteBaseline(db); err != nil {
				return res, fmt.Errorf("sqlite baseline: %w", err)
			}
			for _, v := range sqliteBaselineVersions {
				if err := recordVersionSQL(db, v.Version, v.Name); err != nil {
					return res, fmt.Errorf("record v%d: %w", v.Version, err)
				}
				res.Applied = append(res.Applied, v.Version)
			}
			res.ToVersion = CurrentSchemaVersion
			// Apply v12+ steps on top of the baseline (usually no-ops).
			if err := applySQLiteSteps(db, CurrentSchemaVersion, &res); err != nil {
				return res, err
			}
			return res, nil
		}
		return res, fmt.Errorf("sqlite: database has tables but schema_migrations is empty; refuse to guess history")
	}

	if cur < CurrentSchemaVersion {
		return res, fmt.Errorf("sqlite: version %d is below baseline %d and is not a supported upgrade path; use a new file or export/import", cur, CurrentSchemaVersion)
	}

	// 已在 v11 的库继续应用 v12+ steps（不再要求 cur==CurrentSchemaVersion）。
	pending, err := pendingSQLiteSteps(db, cur)
	if err != nil {
		return res, err
	}
	if len(pending) == 0 {
		res.ToVersion = cur
		return res, nil
	}

	// 非空库在应用结构变更前必须先快照；失败即中止。
	if err := snapshotBeforeSQLiteSteps(db, opts, len(pending)); err != nil {
		return res, err
	}

	for _, s := range pending {
		log.Printf("migrate/sqlite: applying v%d %s", s.Version, s.Name)
		if err := s.SQLite(db); err != nil {
			return res, fmt.Errorf("migration v%d (%s) failed: %w", s.Version, s.Name, err)
		}
		if err := recordVersionSQL(db, s.Version, s.Name); err != nil {
			return res, fmt.Errorf("record v%d: %w", s.Version, err)
		}
		res.Applied = append(res.Applied, s.Version)
	}
	res.ToVersion = pending[len(pending)-1].Version
	return res, nil
}

// pendingSQLiteSteps 返回尚未应用的 v12+ SQLite 步骤。
func pendingSQLiteSteps(db *sqlx.DB, from int) ([]Step, error) {
	var out []Step
	for _, s := range steps {
		if s.Version <= from || s.SQLite == nil {
			continue
		}
		var exists int
		if err := db.Get(&exists, `SELECT COUNT(*) FROM schema_migrations WHERE version=?`, s.Version); err != nil {
			return nil, err
		}
		if exists > 0 {
			continue
		}
		out = append(out, s)
	}
	return out, nil
}

// snapshotBeforeSQLiteSteps 在非空库上生成并校验升级前快照。
// 未配置快照机制时按 fail-closed 处理：拒绝在无备份的情况下改结构。
func snapshotBeforeSQLiteSteps(db *sqlx.DB, opts SQLiteOptions, changes int) error {
	empty, err := sqliteDatabaseEmpty(db)
	if err != nil {
		log.Printf("migrate/sqlite: check empty failed: %v", err)
		empty = false
	}
	if empty {
		return nil
	}
	if opts.DBPath == "" || opts.SnapshotDir == "" || opts.Snapshot == nil {
		return fmt.Errorf("数据库非空且有 %d 项待升级，但未配置 SQLite 升级前快照；为避免无备份升级已中止", changes)
	}
	path, err := opts.Snapshot(opts.DBPath, opts.SnapshotDir)
	if err != nil {
		return fmt.Errorf("升级前快照失败，已中止迁移: %w", err)
	}
	log.Printf("migrate/sqlite: pre-upgrade snapshot saved: %s", path)
	return nil
}

func applySQLiteSteps(db *sqlx.DB, from int, res *Result) error {
	pending, err := pendingSQLiteSteps(db, from)
	if err != nil {
		return err
	}
	for _, s := range pending {
		log.Printf("migrate/sqlite: applying v%d %s", s.Version, s.Name)
		if err := s.SQLite(db); err != nil {
			return fmt.Errorf("migration v%d (%s) failed: %w", s.Version, s.Name, err)
		}
		if err := recordVersionSQL(db, s.Version, s.Name); err != nil {
			return fmt.Errorf("record v%d: %w", s.Version, err)
		}
		res.Applied = append(res.Applied, s.Version)
		res.ToVersion = s.Version
	}
	return nil
}

func applySQLiteBaseline(db *sqlx.DB) error {
	return execAll(db, sqliteBaselineV11)
}

func execAll(db *sqlx.DB, stmts []string) error {
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("%s: %w", firstLine(stmt), err)
		}
	}
	return nil
}

func firstLine(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			return s[:i]
		}
	}
	return s
}

// currentVersionSQL 在 MySQL / SQLite 上都可用（无 information_schema）。
func currentVersionSQL(db *sqlx.DB) (int, error) {
	var v sql.NullInt64
	if err := db.Get(&v, `SELECT MAX(version) FROM schema_migrations`); err != nil {
		return 0, err
	}
	if !v.Valid {
		return 0, nil
	}
	return int(v.Int64), nil
}

func recordVersionSQL(db *sqlx.DB, version int, name string) error {
	_, err := db.Exec(`INSERT OR IGNORE INTO schema_migrations (version, name) VALUES (?, ?)`, version, name)
	return err
}

func sqliteDatabaseEmpty(db *sqlx.DB) (bool, error) {
	var n int
	err := db.Get(&n, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name <> 'schema_migrations'`)
	return n == 0, err
}
