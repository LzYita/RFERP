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
		"SQLite 数据库快照（.db）",
		"MySQL SQL 备份（.sql）",
		"整库恢复",
		"追加 INSERT",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("backup screen copy missing %q", want)
		}
	}
	for _, misleading := range []string{"mysqldump", "导出为 SQL 文件", "SQL 文件中提取 INSERT"} {
		if strings.Contains(content, misleading) {
			t.Errorf("backup screen still contains backend-inaccurate copy %q", misleading)
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
