package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"app/internal/service"
)

type App struct {
	svc       *service.Service
	window    fyne.Window
	dashboard *DashboardScreen
	stats     *StatsScreen
	product   *ProductScreen
	part      *PartScreen
	bom       *BOMScreen
	batch     *BatchScreen
	audit     *AuditScreen
	backup    *BackupScreen

	navBtns  []*widget.Button
	content  *fyne.Container
	pages    []fyne.CanvasObject
	selected int
}

type navItem struct {
	label string
	icon  fyne.Resource
}

func navItems() []navItem {
	return []navItem{
		{"工作台", theme.HomeIcon()},
		{"统计分析", theme.MediaPlayIcon()},
		{"产品管理", theme.ComputerIcon()},
		{"零件管理", theme.StorageIcon()},
		{"BOM管理", theme.ListIcon()},
		{"批次追溯", theme.HistoryIcon()},
		{"操作记录", theme.InfoIcon()},
		{"备份导出", theme.DownloadIcon()},
	}
}

func NewApp(svc *service.Service, w fyne.Window) *App {
	a := &App{svc: svc, window: w, selected: 0}
	a.dashboard = NewDashboardScreen(svc, w)
	a.stats = NewStatsScreen(svc, w)
	a.product = NewProductScreen(svc, w)
	a.part = NewPartScreen(svc, w)
	a.bom = NewBOMScreen(svc, w)
	a.batch = NewBatchScreen(svc, w)
	a.audit = NewAuditScreen(svc, w)
	a.backup = NewBackupScreen(svc, w)
	return a
}

func (a *App) buildSidebar() fyne.CanvasObject {
	// 顶部品牌区
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

	items := navItems()
	btns := make([]fyne.CanvasObject, 0, len(items)+2)
	btns = append(btns, brandContainer)

	for i, item := range items {
		idx := i
		btn := widget.NewButtonWithIcon(item.label, item.icon, func() { a.Select(idx) })
		btn.Importance = widget.MediumImportance
		btn.Alignment = widget.ButtonAlignLeading
		a.navBtns = append(a.navBtns, btn)
		btns = append(btns, btn)
	}

	sep := canvas.NewRectangle(clrBorder)
	sep.SetMinSize(fyne.NewSize(0, 1))

	navBox := container.NewVBox(btns...)
	navBox = container.NewPadded(navBox)

	sidebar := container.NewBorder(nil, nil, nil, nil, navBox)
	return sidebar
}

func (a *App) Select(idx int) {
	if idx < 0 || idx >= len(a.pages) {
		return
	}
	a.selected = idx
	if a.content != nil {
		a.content.Objects = []fyne.CanvasObject{a.pages[idx]}
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
	switch idx {
	case 0:
		a.dashboard.Refresh()
	case 1:
		a.stats.Refresh()
	case 2:
		a.product.Refresh()
	case 3:
		a.part.Refresh()
	case 4:
		a.bom.Refresh()
	case 5:
		a.batch.Refresh()
	case 6:
		a.audit.Refresh()
	}
}

func (a *App) BuildUI() fyne.CanvasObject {
	a.pages = []fyne.CanvasObject{
		a.dashboard.Build(),
		a.stats.Build(),
		a.product.Build(),
		a.part.Build(),
		a.bom.Build(),
		a.batch.Build(),
		a.audit.Build(),
		a.backup.Build(),
	}

	a.content = container.NewMax(a.pages[0])

	// 侧栏底色
	sideBg := canvas.NewRectangle(clrSurface)
	sideBg.SetMinSize(fyne.NewSize(210, 0))
	sidebar := container.NewStack(sideBg, a.buildSidebar())

	divider := canvas.NewRectangle(clrBorder)
	divider.SetMinSize(fyne.NewSize(1, 0))

	sideBar := container.NewBorder(nil, nil, nil, nil, sidebar)

	layout3 := container.NewBorder(nil, nil, container.NewHBox(sideBar, divider), nil, a.content)

	a.Select(0)
	return layout3
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
