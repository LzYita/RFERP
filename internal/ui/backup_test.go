package ui

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"app/internal/auth"
	"app/internal/model"
	"app/internal/service"
)

func TestBackupScreenUsesBackendAccurateCopy(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	previousUser := auth.Current()
	auth.SetCurrent(&model.User{Role: string(auth.RoleAdmin)})
	defer auth.SetCurrent(previousUser)

	screen := NewBackupScreen(service.NewWithSnapshot(nil, nil, nil), nil)
	root := screen.Build()
	var texts []string
	if scroll, ok := root.(*container.Scroll); ok {
		collectBackupScreenText(scroll.Content, &texts)
	} else {
		t.Fatalf("Build returned %T, want *container.Scroll", root)
	}
	content := strings.Join(texts, "\n")

	for _, want := range []string{
		"一键备份",
		"备份当前数据库",
		"SQLite 快照（.db）",
		"整库恢复",
		"操作前会自动生成",
		"账号、迁移记录与数据库身份",
		"输入确认口令",
		"覆盖现有数据",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("backup screen copy missing %q", want)
		}
	}
	for _, misleading := range []string{
		"mysqldump",
		"导出为 SQL 文件",
		"SQL 文件中提取 INSERT",
		"追加 INSERT",
		"仅追加",
		"导入完成",
		"删除所有表",
		"所有表中的数据已被清空",
	} {
		if strings.Contains(content, misleading) {
			t.Errorf("backup screen still contains inaccurate copy %q", misleading)
		}
	}
}

func TestRestoreTokenIsDerivedFromBackupTimestamp(t *testing.T) {
	cases := map[string]string{
		`D:\backup\backup_20260925_101530.sql`:      "restore 20260925_101530",
		`D:\backup\backup_20260925_101530.db`:       "restore 20260925_101530",
		`D:\backup\pre_restore_20260101_000000.sql`: "restore 20260101_000000",
		`D:\backup\pre_clear_20260101_000000.sql`:   "restore 20260101_000000",
		`D:\backup\arbitrary.sql`:                   "",
		`D:\backup\notes.txt`:                       "",
	}
	for path, want := range cases {
		if got := restoreTokenFor(path); got != want {
			t.Errorf("restoreTokenFor(%q) = %q, want %q", path, got, want)
		}
	}
}

func collectBackupScreenText(obj fyne.CanvasObject, texts *[]string) {
	switch value := obj.(type) {
	case *widget.Label:
		*texts = append(*texts, value.Text)
	case *widget.Button:
		*texts = append(*texts, value.Text)
	}
	if container, ok := obj.(*fyne.Container); ok {
		for _, child := range container.Objects {
			collectBackupScreenText(child, texts)
		}
	}
}
