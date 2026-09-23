package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"app/internal/auth"
	"app/internal/config"
	"app/internal/usecase"
)

type App struct {
	svc       usecase.Applications
	cfg       *config.Config
	window    fyne.Window
	onLogout  func()
	dashboard *DashboardScreen
	stats     *StatsScreen
	product   *ProductScreen
	part      *PartScreen
	bom       *BOMScreen
	batch     *BatchScreen
	audit     *AuditScreen
	backup    *BackupScreen
	users     *UsersScreen

	navItems []navItem
	navBtns  []*widget.Button
	content  *fyne.Container
	pages    []pageDef
	selected int
}

type navItem struct {
	module string
	label  string
	icon   fyne.Resource
}

type pageDef struct {
	item    navItem
	page    fyne.CanvasObject
	refresh func()
}

func NewApp(svc usecase.Applications, cfg *config.Config, w fyne.Window, onLogout func()) *App {
	a := &App{svc: svc, cfg: cfg, window: w, selected: 0, onLogout: onLogout}
	a.dashboard = NewDashboardScreen(svc, w)
	a.stats = NewStatsScreen(svc, w)
	a.product = NewProductScreen(svc, w)
	a.part = NewPartScreen(svc, w)
	a.bom = NewBOMScreen(svc, w)
	a.batch = NewBatchScreen(svc, w)
	a.audit = NewAuditScreen(svc, w)
	a.backup = NewBackupScreen(svc, w)
	a.users = NewUsersScreen(svc, w)
	return a
}

func (a *App) buildSidebar() fyne.CanvasObject {
	brandBg := canvas.NewRectangle(clrPrimary)
	brandBg.SetMinSize(fyne.NewSize(0, 72))

	logoImg := canvas.NewImageFromResource(appLogoPng)
	logoImg.SetMinSize(fyne.NewSize(36, 36))
	logoImg.FillMode = canvas.ImageFillContain

	brand := canvas.NewText("RFERP-仁风仓库管理系统", colorWhite)
	brand.TextSize = 13
	brand.TextStyle = fyne.TextStyle{Bold: true}
	brand.Alignment = fyne.TextAlignCenter

	brandBox := container.NewVBox(logoImg, brand)
	brandBox = container.NewPadded(brandBox)
	brandContainer := container.NewStack(brandBg, container.NewCenter(brandBox))

	btns := make([]fyne.CanvasObject, 0, len(a.navItems)+2)
	btns = append(btns, brandContainer)

	for i, item := range a.navItems {
		idx := i
		btn := widget.NewButtonWithIcon(item.label, item.icon, func() { a.Select(idx) })
		btn.Importance = widget.MediumImportance
		btn.Alignment = widget.ButtonAlignLeading
		a.navBtns = append(a.navBtns, btn)
		btns = append(btns, btn)
	}

	navBox := container.NewVBox(btns...)
	navBox = container.NewPadded(navBox)

	footer := a.buildSidebarFooter()
	return container.NewBorder(nil, footer, nil, nil, navBox)
}

func (a *App) buildSidebarFooter() fyne.CanvasObject {
	name := auth.OperatorName()
	role := auth.CurrentRole().Label()

	nameLbl := widget.NewLabelWithStyle(name, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	roleLbl := widget.NewLabel(role + " · " + auth.Current().Username)

	logoutBtn := widget.NewButtonWithIcon("退出登录", theme.LogoutIcon(), func() {
		auth.Logout()
		if a.onLogout != nil {
			a.onLogout()
		}
	})
	logoutBtn.Importance = widget.LowImportance

	info := container.NewVBox(nameLbl, roleLbl)
	box := container.NewVBox(widget.NewSeparator(), container.NewPadded(info), container.NewPadded(logoutBtn))
	return box
}

func (a *App) Select(idx int) {
	if idx < 0 || idx >= len(a.pages) {
		return
	}
	a.selected = idx
	if a.content != nil {
		a.content.Objects = []fyne.CanvasObject{a.pages[idx].page}
		a.content.Refresh()
	}
	for i, b := range a.navBtns {
		if i == idx {
			b.Importance = widget.HighImportance
		} else {
			b.Importance = widget.MediumImportance
		}
		b.Refresh()
	}
	if a.pages[idx].refresh != nil {
		a.pages[idx].refresh()
	}
}

func (a *App) BuildUI() fyne.CanvasObject {
	defs := []pageDef{
		{navItem{auth.ModuleDashboard, "工作台", theme.HomeIcon()}, a.dashboard.Build(), a.dashboard.Refresh},
		{navItem{auth.ModuleStats, "统计分析", theme.MediaPlayIcon()}, a.stats.Build(), a.stats.Refresh},
		{navItem{auth.ModuleProducts, "产品管理", theme.ComputerIcon()}, a.product.Build(), a.product.Refresh},
		{navItem{auth.ModuleParts, "零件管理", theme.StorageIcon()}, a.part.Build(), a.part.Refresh},
		{navItem{auth.ModuleBOM, "BOM管理", theme.ListIcon()}, a.bom.Build(), a.bom.Refresh},
		{navItem{auth.ModuleBatch, "批次追溯", theme.HistoryIcon()}, a.batch.Build(), a.batch.Refresh},
		{navItem{auth.ModuleAudit, "操作记录", theme.InfoIcon()}, a.audit.Build(), a.audit.Refresh},
		{navItem{auth.ModuleBackup, "备份导出", theme.DownloadIcon()}, a.backup.Build(), nil},
		{navItem{auth.ModuleUsers, "用户管理", theme.AccountIcon()}, a.users.Build(), a.users.Refresh},
	}

	a.navItems = nil
	a.pages = nil
	for _, d := range defs {
		if auth.CanRead(d.item.module) {
			a.navItems = append(a.navItems, d.item)
			a.pages = append(a.pages, d)
		}
	}

	if len(a.pages) == 0 {
		empty := container.NewCenter(widget.NewLabel("当前账号没有可访问的模块，请联系管理员。"))
		a.content = container.NewMax(empty)
	} else {
		a.content = container.NewMax(a.pages[0].page)
	}

	sideBg := canvas.NewRectangle(clrSurface)
	sideBg.SetMinSize(fyne.NewSize(210, 0))
	sidebar := container.NewStack(sideBg, a.buildSidebar())

	divider := canvas.NewRectangle(clrBorder)
	divider.SetMinSize(fyne.NewSize(1, 0))

	sideBar := container.NewBorder(nil, nil, nil, nil, sidebar)
	layout := container.NewBorder(nil, nil, container.NewHBox(sideBar, divider), nil, a.content)

	if len(a.pages) > 0 {
		a.Select(0)
	}
	return layout
}

func (a *App) RefreshAll() {
	a.dashboard.Refresh()
	a.stats.Refresh()
	a.product.Refresh()
	a.part.Refresh()
	a.bom.Refresh()
	a.batch.Refresh()
	a.audit.Refresh()
}
