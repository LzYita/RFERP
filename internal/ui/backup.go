package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"app/internal/auth"
	"app/internal/dbfile"
	"app/internal/nativefiledialog"
	"app/internal/paths"
	"app/internal/update"
	"app/internal/usecase"
)

type BackupScreen struct {
	svc          usecase.Applications
	window       fyne.Window
	backupPath   *widget.Entry
	auditPath    *widget.Entry
	allDataPath  *widget.Entry
	startDate    *widget.Entry
	endDate      *widget.Entry
	clearConfirm *widget.Entry
	importPath   *widget.Entry

	dataDirLabel *widget.Label
	backupHint   *widget.Label
	auditHint    *widget.Label
	allHint      *widget.Label
}

func NewBackupScreen(svc usecase.Applications, w fyne.Window) *BackupScreen {
	return &BackupScreen{svc: svc, window: w}
}

func makePathEntry(initial string) *widget.Entry {
	e := widget.NewEntry()
	e.SetText(initial)
	e.Disable()
	return e
}

func (s *BackupScreen) Build() fyne.CanvasObject {
	title := widget.NewLabelWithStyle("数据库备份与导出", fyne.TextAlignCenter, fyne.TextStyle{Bold: true})

	// ---- 数据目录 ----
	s.dataDirLabel = widget.NewLabel(s.svc.DataDir())
	s.dataDirLabel.Wrapping = fyne.TextWrapWord

	dirTitle := widget.NewLabelWithStyle("数据目录", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	dirDesc := widget.NewLabel("备份与导出文件的存放位置：")
	dirDesc.Wrapping = fyne.TextWrapWord

	pathBg := canvas.NewRectangle(clrHeader)
	pathBg.StrokeColor = clrBorder
	pathBg.StrokeWidth = 1
	pathBg.CornerRadius = 6
	pathCard := container.NewStack(pathBg, container.NewPadded(s.dataDirLabel))

	dirBox := container.NewVBox(
		container.NewHBox(widget.NewIcon(theme.FolderIcon()), dirTitle),
		dirDesc,
	)
	if auth.CanWrite(auth.ModuleBackup) {
		changeDirBtn := widget.NewButtonWithIcon("更改数据目录", theme.FolderOpenIcon(), s.doChangeDataDir)
		changeDirBtn.Importance = widget.HighImportance
		dirBox.Add(container.NewBorder(nil, nil, nil, changeDirBtn, pathCard))
	} else {
		dirBox.Add(pathCard)
	}

	// ---- 一键备份 ----
	backupPath := s.backupPath
	if backupPath == nil {
		backupPath = makePathEntry("尚未备份")
		s.backupPath = backupPath
	}

	backupBtn := widget.NewButtonWithIcon("一键备份", theme.DownloadIcon(), s.doBackup)
	backupBtn.Importance = widget.HighImportance

	s.backupHint = widget.NewLabel(fmt.Sprintf("备份当前数据库，文件保存到 %s", paths.BackupDir()))
	s.backupHint.Wrapping = fyne.TextWrapWord
	backupBox := container.NewVBox(
		widget.NewLabelWithStyle("一 键 备 份", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
		s.backupHint,
		backupBtn,
		container.NewBorder(nil, nil, widget.NewLabelWithStyle("备份文件:", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}), nil, backupPath),
	)

	// ---- 导入备份 ----
	importPath := s.importPath
	if importPath == nil {
		importPath = widget.NewEntry()
		importPath.SetPlaceHolder("请选择或粘贴 .sql 备份文件路径")
		s.importPath = importPath
	}
	browseBtn := widget.NewButtonWithIcon("浏览", theme.FolderOpenIcon(), s.doBrowse)
	importBtn := widget.NewButtonWithIcon("开始导入", theme.UploadIcon(), s.doImport)
	importBtn.Importance = widget.WarningImportance

	importBox := container.NewVBox(
		widget.NewLabelWithStyle("导 入 备 份", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
		widget.NewLabel("SQLite 数据库快照（.db）会整库恢复并重启应用；MySQL SQL 备份（.sql）仅追加 INSERT，不覆盖已有数据。"),
		container.NewBorder(nil, nil, nil, browseBtn, importPath),
		importBtn,
	)

	// ---- 审计日志CSV ----
	s.startDate = widget.NewEntry()
	s.startDate.SetPlaceHolder("YYYY-MM-DD")
	s.startDate.SetText(time.Now().Format("2006-01-02"))

	s.endDate = widget.NewEntry()
	s.endDate.SetPlaceHolder("YYYY-MM-DD")
	s.endDate.SetText(time.Now().Format("2006-01-02"))

	auditPath := s.auditPath
	if auditPath == nil {
		auditPath = makePathEntry("尚未导出")
		s.auditPath = auditPath
	}

	auditBtn := widget.NewButtonWithIcon("导出审计日志CSV", theme.DocumentIcon(), s.doExportAudit)
	auditBtn.Importance = widget.MediumImportance

	dateRow := container.NewGridWithColumns(2,
		container.NewBorder(nil, nil, widget.NewLabel("开始: "), nil, s.startDate),
		container.NewBorder(nil, nil, widget.NewLabel("结束: "), nil, s.endDate),
	)

	// ---- 清空数据库 ----
	clearConfirm := s.clearConfirm
	if clearConfirm == nil {
		clearConfirm = widget.NewEntry()
		clearConfirm.SetPlaceHolder("请在输入框输入 drop 确认清空")
		s.clearConfirm = clearConfirm
	}
	clearBtn := widget.NewButtonWithIcon("清空数据库（危险操作）", theme.DeleteIcon(), s.doClear)
	clearBtn.Importance = widget.DangerImportance

	clearBox := container.NewVBox(
		widget.NewLabelWithStyle("清 空 数 据 库", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
		widget.NewLabel("警告：此操作将删除所有表中的全部数据（保留表结构），不可恢复！"),
		container.NewBorder(nil, nil, widget.NewLabel("输入 drop 确认: "), nil, clearConfirm),
		clearBtn,
	)

	s.auditHint = widget.NewLabel(fmt.Sprintf("按日期范围导出操作记录（CSV），可用 Excel 打开，保存到 %s", filepath.Join(paths.ExportDir(), "audit_log")))
	s.auditHint.Wrapping = fyne.TextWrapWord
	auditBox := container.NewVBox(
		widget.NewLabelWithStyle("审计日志导出 (按日期)", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
		s.auditHint,
		dateRow,
		auditBtn,
		container.NewBorder(nil, nil, widget.NewLabelWithStyle("导出文件:", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}), nil, auditPath),
	)

	// ---- 全部数据CSV ----
	allDataPath := s.allDataPath
	if allDataPath == nil {
		allDataPath = makePathEntry("尚未导出")
		s.allDataPath = allDataPath
	}

	allBtn := widget.NewButtonWithIcon("导出全部数据CSV", theme.StorageIcon(), s.doExportAll)
	allBtn.Importance = widget.MediumImportance

	s.allHint = widget.NewLabel(fmt.Sprintf("将所有表(产品/零件/BOM/批次/追溯/日志)导出为单独 CSV 文件，保存到 %s", filepath.Join(paths.ExportDir(), "all_data")))
	s.allHint.Wrapping = fyne.TextWrapWord
	allBox := container.NewVBox(
		widget.NewLabelWithStyle("全部数据导出 (CSV)", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
		s.allHint,
		allBtn,
		container.NewBorder(nil, nil, widget.NewLabelWithStyle("导出目录:", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}), nil, allDataPath),
	)

	items := []fyne.CanvasObject{title, widget.NewSeparator(), dirBox, widget.NewSeparator()}
	if auth.CanWrite(auth.ModuleBackup) {
		items = append(items,
			backupBox, widget.NewSeparator(),
			importBox, widget.NewSeparator(),
			clearBox, widget.NewSeparator(),
		)
	}
	items = append(items, auditBox, widget.NewSeparator(), allBox)
	content := container.NewVBox(items...)

	return container.NewScroll(content)
}

func (s *BackupScreen) doClear() {
	typed := s.clearConfirm.Text
	if typed != "drop" {
		dialog.ShowInformation("确认失败", "请在输入框中准确输入 drop 以确认清空操作", s.window)
		return
	}
	dialog.NewConfirm("最终警告", "确定要清空数据库所有数据吗？此操作不可恢复！", func(ok bool) {
		if !ok {
			return
		}
		if err := s.svc.ClearDatabase(); err != nil {
			showError(s.window, "清空失败", err)
			return
		}
		s.clearConfirm.SetText("")
		dialog.ShowInformation("清空完成", "所有表中的数据已被清空", s.window)
	}, s.window).Show()
}

func (s *BackupScreen) doChangeDataDir() {
	dir, ok := nativefiledialog.PickFolder("请选择数据目录")
	if !ok || dir == "" {
		return
	}
	if err := s.svc.SetDataDir(dir); err != nil {
		showError(s.window, "修改数据目录失败", err)
		return
	}
	s.refreshPaths()
	dialog.ShowInformation("已修改", "数据目录已更改为：\n"+s.svc.DataDir(), s.window)
}

func (s *BackupScreen) refreshPaths() {
	if s.dataDirLabel != nil {
		s.dataDirLabel.SetText(s.svc.DataDir())
	}
	if s.backupHint != nil {
		s.backupHint.SetText(fmt.Sprintf("备份当前数据库，文件保存到 %s", paths.BackupDir()))
	}
	if s.auditHint != nil {
		s.auditHint.SetText(fmt.Sprintf("按日期范围导出操作记录（CSV），可用 Excel 打开，保存到 %s", filepath.Join(paths.ExportDir(), "audit_log")))
	}
	if s.allHint != nil {
		s.allHint.SetText(fmt.Sprintf("将所有表(产品/零件/BOM/批次/追溯/日志)导出为单独 CSV 文件，保存到 %s", filepath.Join(paths.ExportDir(), "all_data")))
	}
}

func (s *BackupScreen) getBackupDir() string {
	return paths.BackupDir()
}

func (s *BackupScreen) getExportDir() string {
	return paths.ExportDir()
}

func (s *BackupScreen) getAuditExportDir() string {
	return filepath.Join(s.getExportDir(), "audit_log")
}

func (s *BackupScreen) getAllDataExportDir() string {
	return filepath.Join(s.getExportDir(), "all_data")
}

func (s *BackupScreen) doBackup() {
	dir := s.getBackupDir()
	path, err := s.svc.BackupDatabase(dir)
	if err != nil {
		showError(s.window, "备份失败", err)
		return
	}
	s.backupPath.SetText(path)
	dialog.ShowInformation("备份完成",
		fmt.Sprintf("数据库备份成功！\n保存路径：\n%s", path), s.window)
}

func (s *BackupScreen) doBrowse() {
	backupDir := s.getBackupDir()
	var files []string
	filepath.Walk(backupDir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		name := strings.ToLower(info.Name())
		if strings.HasSuffix(name, ".sql") || strings.HasSuffix(name, ".db") {
			files = append(files, p)
		}
		return nil
	})
	if len(files) == 0 {
		dialog.ShowInformation("提示", fmt.Sprintf("在 %s 下未找到 .sql / .db 备份文件", backupDir), s.window)
		return
	}
	sort.Strings(files)
	var names []string
	for _, f := range files {
		rel, _ := filepath.Rel(backupDir, f)
		names = append(names, rel)
	}
	list := widget.NewList(
		func() int { return len(names) },
		func() fyne.CanvasObject { return widget.NewLabel("xxxxxxxx.sql") },
		func(i widget.ListItemID, o fyne.CanvasObject) {
			o.(*widget.Label).SetText(names[i])
		},
	)
	list.OnSelected = func(id widget.ListItemID) {
		s.importPath.SetText(files[id])
		dialog.ShowInformation("已选择", fmt.Sprintf("已选择文件：\n%s", files[id]), s.window)
	}
	pop := dialog.NewCustom("选择备份文件 - "+backupDir, "关闭", list, s.window)
	pop.Resize(fyne.NewSize(600, 400))
	pop.Show()
}

func (s *BackupScreen) doImport() {
	filePath := s.importPath.Text
	if filePath == "" {
		dialog.ShowInformation("提示", "请先选择或输入备份文件路径", s.window)
		return
	}
	if _, err := os.Stat(filePath); err != nil {
		dialog.ShowInformation("提示", "文件不存在或无法访问，请检查路径", s.window)
		return
	}
	// D-014: snapshot restore is whole-DB rollback, never a merge.
	// 类型按文件内容判断（与 Service.RestoreDatabase 同一口径），不看扩展名。
	isSnapshot := dbfile.IsSQLiteSnapshot(filePath)
	msg := fmt.Sprintf("即将从以下备份恢复：\n%s\n\n", filePath)
	if isSnapshot {
		msg += "这是 SQLite 整库快照。恢复=整库回到该备份时刻，不会与当前数据合并。\n" +
			"操作前会把当前数据库另存为 <数据库文件>.before-restore-<时间戳>。\n\n" +
			"完成后应用将自动重启，请确认现在继续。"
	} else {
		msg += "即将从以下文件追加导入数据（仅执行 INSERT 语句）："
	}
	dialog.NewConfirm("确认恢复", msg,
		func(confirm bool) {
			if !confirm {
				return
			}
			success, failed, err := s.svc.RestoreDatabase(filePath)
			if err != nil {
				showError(s.window, "恢复失败", err)
				return
			}
			if isSnapshot {
				// File is replaced and handles are closed — relaunch so the next
				// start opens the restored DB cleanly (avoids a half-dead session).
				if rerr := update.RestartApp(); rerr != nil {
					dialog.ShowInformation("已整库回退，请手动重启",
						"已整库回到所选备份时刻（未与当前数据合并）。\n"+
							"恢复前的数据库已另存为 <数据库文件>.before-restore-<时间戳>。\n"+
							"自动重启失败："+rerr.Error()+"\n\n"+
							"请关闭并重新打开 RFERP 后再继续操作。",
						s.window)
					return
				}
				os.Exit(0)
				return
			}
			msg := fmt.Sprintf("成功导入 %d 条记录", success)
			if failed > 0 {
				msg += fmt.Sprintf("，%d 条跳过（可能已存在）", failed)
			}
			msg += fmt.Sprintf("\n文件：%s", filePath)
			dialog.ShowInformation("导入完成", msg, s.window)
		}, s.window).Show()
}

func (s *BackupScreen) doExportAudit() {
	start, err := time.Parse("2006-01-02", s.startDate.Text)
	if err != nil {
		dialog.ShowInformation("提示", "开始日期格式错误，请使用 YYYY-MM-DD 格式", s.window)
		return
	}
	end, err := time.Parse("2006-01-02", s.endDate.Text)
	if err != nil {
		dialog.ShowInformation("提示", "结束日期格式错误，请使用 YYYY-MM-DD 格式", s.window)
		return
	}
	if end.Before(start) {
		dialog.ShowInformation("提示", "结束日期不能早于开始日期", s.window)
		return
	}
	dir := filepath.Join(s.getAuditExportDir(), time.Now().Format("20060102_150405"))
	if err := os.MkdirAll(dir, 0755); err != nil {
		showError(s.window, "创建目录失败", err)
		return
	}
	filePath := filepath.Join(dir, fmt.Sprintf("audit_log_%s_%s.csv",
		start.Format("20060102"), end.Format("20060102")))

	count, err := s.svc.ExportAuditLogCSV(start, end, filePath)
	if err != nil {
		showError(s.window, "导出失败", err)
		return
	}
	s.auditPath.SetText(filePath)
	dialog.ShowInformation("导出完成",
		fmt.Sprintf("共导出 %d 条审计日志\n保存路径：\n%s", count, filePath), s.window)
}

func (s *BackupScreen) doExportAll() {
	dir := s.getAllDataExportDir()
	files, subDir, err := s.svc.ExportAllDataCSV(dir)
	if err != nil {
		showError(s.window, "导出失败", err)
		return
	}
	msg := fmt.Sprintf("导出完成！已保存到：\n%s\n\n文件列表：", subDir)
	for name, path := range files {
		msg += fmt.Sprintf("\n%s → %s", name, path)
	}
	s.allDataPath.SetText(subDir)
	dialog.ShowInformation("导出完成", msg, s.window)
}
