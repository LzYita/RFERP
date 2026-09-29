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
	svc           usecase.Applications
	window        fyne.Window
	backupPath    *widget.Entry
	auditPath     *widget.Entry
	allDataPath   *widget.Entry
	startDate     *widget.Entry
	endDate       *widget.Entry
	clearConfirm  *widget.Entry
	importPath    *widget.Entry
	restoreToken  *widget.Entry
	importBtn     *widget.Button
	clearBtn      *widget.Button
	running       bool
	restorePrompt string

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
	importBtn := widget.NewButtonWithIcon("整库恢复（覆盖现有数据）", theme.UploadIcon(), s.doImport)
	importBtn.Importance = widget.DangerImportance
	s.importBtn = importBtn

	restoreToken := s.restoreToken
	if restoreToken == nil {
		restoreToken = widget.NewEntry()
		restoreToken.SetPlaceHolder("请在下方输入框输入确认口令后恢复")
		s.restoreToken = restoreToken
	}
	s.refreshRestorePrompt()

	importBox := container.NewVBox(
		widget.NewLabelWithStyle("整 库 恢 复", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
		widget.NewLabel("恢复=把数据库整体回到所选备份时刻，当前数据会被完全覆盖（不是追加导入）。"+
			"两种后端一致：SQLite 快照（.db）与 MySQL 备份（.sql）都是整库恢复。"),
		widget.NewLabel("操作前会自动生成一份可回退的副本；账号、迁移记录与数据库身份不会被覆盖。"),
		container.NewBorder(nil, nil, nil, browseBtn, importPath),
		container.NewBorder(nil, nil, widget.NewLabel("输入确认口令: "), nil, restoreToken),
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
	clearBtn := widget.NewButtonWithIcon("清空业务数据（危险操作）", theme.DeleteIcon(), s.doClear)
	clearBtn.Importance = widget.DangerImportance
	s.clearBtn = clearBtn

	clearBox := container.NewVBox(
		widget.NewLabelWithStyle("清 空 业 务 数 据", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
		widget.NewLabel("将清空产品/零件/BOM/批次/追溯/操作记录；账号、迁移记录与数据库身份会保留。"),
		widget.NewLabel("操作前会自动生成一份可回退的副本，因此本次清空是可恢复的。"),
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

// setRunning 在破坏性操作执行期间禁用按钮，防止重复提交。
func (s *BackupScreen) setRunning(running bool) {
	s.running = running
	if s.importBtn != nil {
		if running {
			s.importBtn.Disable()
		} else {
			s.importBtn.Enable()
		}
	}
	if s.clearBtn != nil {
		if running {
			s.clearBtn.Disable()
		} else {
			s.clearBtn.Enable()
		}
	}
}

// doClear 清空业务数据。保留账号、迁移记录与数据库身份。
func (s *BackupScreen) doClear() {
	if s.running {
		return
	}
	typed := s.clearConfirm.Text
	if typed != "drop" {
		dialog.ShowInformation("确认失败", "请在输入框中准确输入 drop 以确认清空操作", s.window)
		return
	}
	dialog.NewConfirm("最终警告", "确定要清空全部业务数据吗？\n\n"+
		"· 产品/零件/BOM/批次/追溯/操作记录会被清空\n"+
		"· 账号、迁移记录与数据库身份会保留\n"+
		"· 操作前会自动生成一份完整副本，可用整库恢复退回",
		func(ok bool) {
			if !ok {
				return
			}
			s.setRunning(true)
			res, err := s.svc.ClearDatabase()
			s.setRunning(false)
			if err != nil {
				showError(s.window, "清空失败", err)
				return
			}
			s.clearConfirm.SetText("")
			msg := fmt.Sprintf("业务数据已清空；账号、迁移记录与数据库身份已保留。\n\n可回退副本：\n%s", res.PreClear)
			if len(res.Cleared) > 0 {
				msg += "\n\n已清空：" + strings.Join(res.Cleared, "、")
			}
			dialog.ShowInformation("清空完成", msg, s.window)
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
		s.refreshRestorePrompt()
		dialog.ShowInformation("已选择", fmt.Sprintf("已选择文件：\n%s\n\n确认口令已自动填好，直接点「整库恢复」即可。", files[id]), s.window)
	}
	pop := dialog.NewCustom("选择备份文件 - "+backupDir, "关闭", list, s.window)
	pop.Resize(fyne.NewSize(600, 400))
	pop.Show()
}

// gateError 是闸门拒绝的原因，携带弹窗标题与文案。
type gateError struct {
	title   string
	message string
}

func (e *gateError) Error() string { return e.title + ": " + e.message }

// restoreGateErr 校验一次恢复请求能否进入确认弹窗。
// 纯函数：不碰 UI、不碰数据库，因此可以被直接测试。
//
// 闸门（#26）：
//
//	G1 文件必须存在、非空——空文件与不存在的路径直接拒绝
//	G2 必须能从文件名识别出备份时间戳，并且口令与之完全匹配
//
// Service.RestoreDatabase 还会再校验一次"完整备份标记"，
// 这里只负责挡住明显的误操作。
func restoreGateErr(filePath, typedToken string) *gateError {
	if filePath == "" {
		return &gateError{"提示", "请先选择或输入备份文件路径"}
	}
	info, err := os.Stat(filePath)
	if err != nil {
		return &gateError{"提示", "文件不存在或无法访问，请检查路径"}
	}
	if info.Size() == 0 {
		return &gateError{"备份文件无效", "所选文件为空，无法用于恢复。\n\n" +
			"请选择本应用「一键备份」或「操作前自动副本」生成的 .sql / .db 文件。"}
	}
	token := restoreTokenFor(filePath)
	if token == "" {
		return &gateError{"无法确认", "无法从文件名识别备份时间戳，为避免误操作已拒绝恢复。\n\n" +
			"仅支持恢复本应用生成的备份：\n· backup_<时间戳>.sql / .db（一键备份）\n" +
			"· pre_restore_<时间戳>.sql（恢复前副本）\n· pre_clear_<时间戳>.sql（清空前副本）"}
	}
	if typedToken != token {
		return &gateError{"确认失败", fmt.Sprintf("请在确认口令输入框中准确输入：\n\n%s\n\n"+
			"该口令对应你选中的备份文件，用于避免恢复错文件。", token)}
	}
	return nil
}

// restoreTokenFor 构造该备份文件对应的确认口令。
// 带时间戳是为了强迫操作者读一遍"到底要恢复哪一份"，
// 避免选了 A 却导了 B。
func restoreTokenFor(filePath string) string {
	ts := backupTimestamp(filePath)
	if ts == "" {
		return ""
	}
	return "restore " + ts
}

// backupTimestamp 从 backup_YYYYMMDD_HHMMSS.sql / .db 文件名中取出时间戳。
func backupTimestamp(filePath string) string {
	name := filepath.Base(filePath)
	name = strings.TrimSuffix(name, filepath.Ext(name))
	for _, prefix := range []string{"backup_", "pre_restore_", "pre_clear_"} {
		if strings.HasPrefix(name, prefix) {
			return strings.TrimPrefix(name, prefix)
		}
	}
	return ""
}

// refreshRestorePrompt 按当前选中的文件刷新口令提示与输入框内容。
func (s *BackupScreen) refreshRestorePrompt() {
	token := restoreTokenFor(s.importPath.Text)
	if s.restoreToken != nil {
		s.restoreToken.SetPlaceHolder("请在下方输入框输入确认口令后恢复")
		if token != "" {
			s.restoreToken.SetText(token)
		} else {
			s.restoreToken.SetText("")
		}
	}
	s.restorePrompt = token
}

// doImport 整库恢复。四道闸门（#26）：
//
//	G1 备份文件必须存在、非空、带 mysqldump 完成标记（由 service 二次校验）
//	G2 必须输入与该文件时间戳匹配的口令
//	G3 逐条列明后果的二次确认
//	G4 执行期间禁用按钮
func (s *BackupScreen) doImport() {
	if s.running {
		return
	}
	filePath := strings.TrimSpace(s.importPath.Text)
	typed := ""
	if s.restoreToken != nil {
		typed = strings.TrimSpace(s.restoreToken.Text)
	}
	// 闸门 G1/G2：文件合法性与确认口令。不通过就不进入确认流程。
	if gateErr := restoreGateErr(filePath, typed); gateErr != nil {
		dialog.ShowInformation(gateErr.title, gateErr.message, s.window)
		return
	}
	// D-014: snapshot restore is whole-DB rollback, never a merge.
	// 类型按文件内容判断（与 Service.RestoreDatabase 同一口径），不看扩展名。
	isSnapshot := dbfile.IsSQLiteSnapshot(filePath)

	// 闸门 G3：逐条列明后果。
	msg := fmt.Sprintf("即将把数据库整库恢复到以下备份时刻：\n%s\n\n", filePath)
	msg += "请确认以下后果：\n"
	if isSnapshot {
		msg += "1. 当前数据会被完全覆盖（整库回到该备份时刻，不与现有数据合并）\n"
	} else {
		msg += "1. 当前业务数据会被完全覆盖（不是追加导入）\n"
	}
	msg += "2. 操作前会自动生成一份完整副本，可用整库恢复退回\n"
	msg += "3. 账号、迁移记录与数据库身份不会被覆盖，账号保持现状\n"
	msg += "4. 操作在单个事务内完成；中途失败会整体回滚，数据库保持恢复前状态\n"
	if isSnapshot {
		msg += "\nSQLite 快照恢复后应用会自动重启。"
	}
	dialog.NewConfirm("最终警告：整库恢复", msg,
		func(confirm bool) {
			if !confirm {
				return
			}
			s.setRunning(true)
			res, rerr := s.svc.RestoreDatabase(filePath)
			s.setRunning(false)
			if rerr != nil {
				showError(s.window, "恢复失败", rerr)
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
			s.restoreToken.SetText("")
			out := fmt.Sprintf("整库恢复完成，共执行 %d 条 INSERT。\n\n恢复后各表行数：\n%s",
				res.Statements, restoreRowsSummary(res.TableCounts))
			if res.PreRestore != "" {
				out += fmt.Sprintf("\n\n可回退副本：\n%s\n如需退回，用该文件再执行一次整库恢复即可。", res.PreRestore)
			}
			dialog.ShowInformation("恢复完成", out, s.window)
		}, s.window).Show()
}

// restoreRowsSummary 返回按表名排序的行数摘要。
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
	return strings.Join(parts, "\n")
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
