package service

import (
	"database/sql"
	"strings"
	"testing"

	"app/internal/paths"
	"app/internal/repository"
)

func TestInsertTargetIsPreservedRecognisesQuotedAndBareNames(t *testing.T) {
	cases := map[string]bool{
		"INSERT INTO `schema_migrations` VALUES (1)":        true,
		"INSERT INTO `users` VALUES (1)":                    true,
		"INSERT INTO `db_identity` (`id`) VALUES ('x')":     true,
		"INSERT INTO `devices` VALUES (1)":                  true,
		"INSERT INTO license VALUES (1)":                    true,
		"INSERT INTO `security_events` VALUES (1)":          true,
		"insert into schema_migrations (version) values(1)": true,
		"INSERT INTO `products` VALUES (1)":                 false,
		"INSERT INTO `parts` (`code`) VALUES ('a')":         false,
		"INSERT INTO `audit_log` VALUES (1)":                false,
	}
	for stmt, want := range cases {
		if got := insertTargetIsPreserved(stmt); got != want {
			t.Errorf("insertTargetIsPreserved(%q) = %v, want %v", stmt, got, want)
		}
	}
}

func TestBusinessAndPreservedTablesDoNotOverlap(t *testing.T) {
	// 两份清单若有交集，恢复时会既删又保留同一张表，语义自相矛盾。
	for _, b := range businessTables {
		if isPreservedTable(b) {
			t.Errorf("table %q is listed as both business and preserved", b)
		}
	}
	// 三张系统表必须都在保留清单里（产品决策：账号不随数据回滚）。
	for _, p := range []string{"users", "schema_migrations", "db_identity", "devices", "license", "security_events"} {
		if !isPreservedTable(p) {
			t.Errorf("system table %q missing from preservedTables", p)
		}
	}
}

func TestPreservedTablesCoverTheTablesRestoreMustNotTouch(t *testing.T) {
	// 明确记录「不清空」的表集合，避免后续有人只删一条就悄悄改变语义。
	joined := strings.Join(preservedTables, ",")
	for _, table := range []string{"users", "schema_migrations", "db_identity", "devices", "license", "security_events"} {
		if !strings.Contains(joined, table) {
			t.Errorf("preservedTables lost %q", table)
		}
	}
}

type restoreLANStateTx struct {
	repository.TxOps
	statements []string
}

func (tx *restoreLANStateTx) Exec(statement string, _ ...any) (sql.Result, error) {
	tx.statements = append(tx.statements, statement)
	return nil, nil
}

func TestRestorePreservesLANSystemTables(t *testing.T) {
	tx := &restoreLANStateTx{}
	content := "INSERT INTO devices VALUES (1);\nINSERT INTO `license` VALUES (1);\n" +
		"INSERT INTO `security_events` VALUES (1);\nINSERT INTO `parts` VALUES (1);"
	count, err := replayDumpInserts(tx, content)
	if err != nil || count != 1 || len(tx.statements) != 1 || tx.statements[0] != "INSERT INTO `parts` VALUES (1);" {
		t.Fatalf("count=%d statements=%v err=%v", count, tx.statements, err)
	}
}

func TestSafetyCopyKeepIsBounded(t *testing.T) {
	// 副本份数必须有上限，否则备份目录会无限增长。
	if paths.SafetyCopyKeep <= 0 {
		t.Fatalf("SafetyCopyKeep = %d, want a positive bound", paths.SafetyCopyKeep)
	}
	if paths.SafetyCopyKeep > 20 {
		t.Fatalf("SafetyCopyKeep = %d, want a small bound", paths.SafetyCopyKeep)
	}
	// 手动备份必须**不**有上限：用户主动留的东西不该被自动清理。
	if paths.AutoBackupKeep <= 0 {
		t.Fatalf("AutoBackupKeep = %d, want a positive bound", paths.AutoBackupKeep)
	}
}
