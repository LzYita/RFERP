package service

import (
	"strings"
	"testing"
)

func TestInsertTargetIsPreservedRecognisesQuotedAndBareNames(t *testing.T) {
	cases := map[string]bool{
		"INSERT INTO `schema_migrations` VALUES (1)":        true,
		"INSERT INTO `users` VALUES (1)":                    true,
		"INSERT INTO `db_identity` (`id`) VALUES ('x')":     true,
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
	for _, p := range []string{"users", "schema_migrations", "db_identity"} {
		if !isPreservedTable(p) {
			t.Errorf("system table %q missing from preservedTables", p)
		}
	}
}

func TestPreservedTablesCoverTheTablesRestoreMustNotTouch(t *testing.T) {
	// 明确记录「不清空」的表集合，避免后续有人只删一条就悄悄改变语义。
	joined := strings.Join(preservedTables, ",")
	for _, table := range []string{"users", "schema_migrations", "db_identity"} {
		if !strings.Contains(joined, table) {
			t.Errorf("preservedTables lost %q", table)
		}
	}
}

func TestSafetyCopyKeepIsBounded(t *testing.T) {
	// 副本份数必须有上限，否则备份目录会无限增长。
	if safetyCopyKeep <= 0 {
		t.Fatalf("safetyCopyKeep = %d, want a positive bound", safetyCopyKeep)
	}
	if safetyCopyKeep > 20 {
		t.Fatalf("safetyCopyKeep = %d, want a small bound", safetyCopyKeep)
	}
}
