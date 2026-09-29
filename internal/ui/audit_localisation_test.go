package ui

import (
	"strings"
	"testing"

	"app/internal/model"
)

func strPtrLocal(s string) *string { return &s }

// #26：整库恢复 / 清空业务数据的操作记录必须显示中文，
// 不能把 RESTORE / CLEAR 直接暴露给用户。
func TestWholeDatabaseAuditActionsAreLocalised(t *testing.T) {
	cases := map[string]string{
		"RESTORE": "整库恢复",
		"CLEAR":   "清空业务数据",
	}
	for action, want := range cases {
		if got := actionLabel(action); got != want {
			t.Errorf("actionLabel(%q) = %q, want %q", action, got, want)
		}
	}
	if got := tableLabel("database"); got != "整库操作" {
		t.Errorf("tableLabel(\"database\") = %q, want 整库操作", got)
	}
	// 既有标签不能被这次改动影响
	for action, want := range map[string]string{
		"INSERT": "新增", "UPDATE": "修改", "DELETE": "删除",
		"STOCK_IN": "入库", "STOCK_DEDUCT": "出库", "STOCK_ADJUST": "盘点",
		"REVOKE": "撤销", "UPDATE_STATUS": "状态变更",
	} {
		if got := actionLabel(action); got != want {
			t.Errorf("actionLabel(%q) = %q, want %q (regression)", action, got, want)
		}
	}
}

func TestWholeDatabaseAuditSummaryIsReadableChinese(t *testing.T) {
	operator := "验收管理员"

	restore := model.AuditLog{
		TableName: "database",
		Action:    "RESTORE",
		Operator:  &operator,
		// 键名与 service.ClearDatabase / RestoreDatabase 写入的审计字段一致
		NewData: &map[string]any{
			"file":        `D:\仓库数据\备份\backup_20260925_101530.sql`,
			"pre_restore": `D:\仓库数据\备份\pre_restore_20260929_110000.sql`,
			"statements":  float64(42),
		},
	}
	summary := shortSummary(restore)
	if !strings.Contains(summary, "整库恢复") {
		t.Errorf("restore summary = %q, want it to start with 整库恢复", summary)
	}
	if !strings.Contains(summary, "backup_20260925_101530.sql") {
		t.Errorf("restore summary = %q, want it to name the restored backup", summary)
	}
	if strings.Contains(summary, `\`) {
		t.Errorf("restore summary = %q, want only the file name, not the whole path", summary)
	}
	if strings.Contains(summary, "RESTORE") {
		t.Errorf("restore summary = %q, must not expose the raw action name", summary)
	}

	clear := model.AuditLog{
		TableName: "database",
		Action:    "CLEAR",
		Operator:  &operator,
		NewData: &map[string]any{
			"pre_clear": `D:\仓库数据\备份\pre_clear_20260929_105500.sql`,
			"cleared":   []string{"parts", "products"},
		},
	}
	summary = shortSummary(clear)
	if !strings.Contains(summary, "清空业务数据") {
		t.Errorf("clear summary = %q, want it to start with 清空业务数据", summary)
	}
	if !strings.Contains(summary, "pre_clear_20260929_105500.sql") {
		t.Errorf("clear summary = %q, want it to name the pre-clear copy", summary)
	}
	if strings.Contains(summary, "CLEAR") {
		t.Errorf("clear summary = %q, must not expose the raw action name", summary)
	}
}

func TestBaseNameKeepsOnlyTheLastSegment(t *testing.T) {
	cases := map[string]string{
		`D:\仓库数据\备份\backup_20260925_101530.sql`: "backup_20260925_101530.sql",
		`C:/data/backup/pre_clear_1.sql`:        "pre_clear_1.sql",
		`backup_20260101_000000.sql`:            "backup_20260101_000000.sql",
		``:                                      "",
	}
	for in, want := range cases {
		if got := baseName(in); got != want {
			t.Errorf("baseName(%q) = %q, want %q", in, got, want)
		}
	}
}
