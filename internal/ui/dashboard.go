package ui

import (
	"fmt"
	"image/color"
	"sort"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"app/internal/model"
	"app/internal/service"
)

type DashboardScreen struct {
	svc         *service.Service
	window      fyne.Window
	cardGrid    *fyne.Container
	warnList    *widget.List
	logTable    *widget.Table
	warnData    []model.Part
	logData     []model.AuditLog
	lastRefresh *widget.Label
}

func NewDashboardScreen(svc *service.Service, w fyne.Window) *DashboardScreen {
	return &DashboardScreen{svc: svc, window: w}
}

func (d *DashboardScreen) Build() fyne.CanvasObject {
	title := canvas.NewText("工作台", clrForeground)
	title.TextSize = 20
	title.TextStyle = fyne.TextStyle{Bold: true}

	d.lastRefresh = widget.NewLabel("")
	d.lastRefresh.TextStyle = fyne.TextStyle{Italic: true}

	refreshBtn := widget.NewButtonWithIcon("刷新数据", theme.ViewRefreshIcon(), d.Refresh)

	header := container.NewBorder(nil, nil, nil,
		container.NewHBox(d.lastRefresh, refreshBtn),
		title)

	// 统计卡片区
	d.cardGrid = container.NewGridWithColumns(4)

	// 预警列表
	warnHeader := widget.NewLabelWithStyle("库存预警", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	d.warnList = widget.NewList(
		func() int { return len(d.warnData) },
		func() fyne.CanvasObject {
			return widget.NewLabel("XXXXXXXXXXXXXXXXXXXX")
		},
		func(id widget.ListItemID, o fyne.CanvasObject) {
			p := d.warnData[id]
			lbl := o.(*widget.Label)
			lbl.TextStyle = fyne.TextStyle{}
			lbl.Alignment = fyne.TextAlignCenter
			lbl.SetText(fmt.Sprintf("[%s] %s   库存:%.2f / 预警:%.2f",
				p.Code, p.Name, p.StockQty, p.WarnQty))
		},
	)
	warnCard := cardPanel(warnHeader, d.warnList)

	// 最近操作
	logHeader := widget.NewLabelWithStyle("最近操作记录", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	d.logTable = widget.NewTable(
		func() (int, int) { return len(d.logData) + 1, 4 },
		func() fyne.CanvasObject { return widget.NewLabel("XXXXXXXXXXXXXXXXXXXX") },
		func(tci widget.TableCellID, o fyne.CanvasObject) {
			cell := o.(*widget.Label)
			if tci.Row == 0 {
				headers := []string{"时间", "对象", "操作", "内容"}
				cell.SetText(headers[tci.Col])
				cell.TextStyle = fyne.TextStyle{Bold: true}
				cell.Alignment = fyne.TextAlignCenter
				return
			}
			idx := tci.Row - 1
			if idx >= len(d.logData) {
				return
			}
			a := d.logData[idx]
			cell.TextStyle = fyne.TextStyle{}
			cell.Alignment = fyne.TextAlignCenter
			switch tci.Col {
			case 0:
				cell.SetText(a.CreatedAt.Format("01-02 15:04"))
			case 1:
				cell.SetText(tableLabel(a.TableName))
			case 2:
				cell.SetText(actionLabel(a.Action))
			case 3:
				cell.SetText(shortSummary(a))
			}
		},
	)
	d.logTable.SetColumnWidth(0, 110)
	d.logTable.SetColumnWidth(1, 80)
	d.logTable.SetColumnWidth(2, 90)
	d.logTable.SetColumnWidth(3, 460)
	logCard := cardPanel(logHeader, d.logTable)

	// 下方双栏
	split := container.NewHSplit(warnCard, logCard)
	split.Offset = 0.35

	body := container.NewBorder(d.cardGrid, nil, nil, nil, split)
	body = container.NewPadded(body)

	content := container.NewBorder(header, nil, nil, nil, body)

	d.Refresh()
	return content
}

func cardPanel(header fyne.CanvasObject, inner fyne.CanvasObject) fyne.CanvasObject {
	line := canvas.NewRectangle(clrPrimary)
	line.SetMinSize(fyne.NewSize(4, 4))

	top := container.NewHBox(line, header)
	body := container.NewPadded(inner)
	content := container.NewBorder(top, nil, nil, nil, body)

	bg := canvas.NewRectangle(clrSurface)
	bg.CornerRadius = 8

	return container.NewStack(bg, content)
}

func statCard(title string, value int, c color.Color, icon fyne.Resource) fyne.CanvasObject {
	num := canvas.NewText(fmt.Sprintf("%d", value), clrForeground)
	num.TextSize = 28
	num.Alignment = fyne.TextAlignCenter
	num.TextStyle = fyne.TextStyle{Bold: true}

	t := canvas.NewText(title, clrForeground2)
	t.Alignment = fyne.TextAlignCenter
	t.TextSize = 13

	iconObj := canvas.NewImageFromResource(icon)
	iconObj.SetMinSize(fyne.NewSize(22, 22))

	iconBox := container.NewHBox(layout.NewSpacer(), iconObj, layout.NewSpacer())

	bg := canvas.NewRectangle(clrSurface)
	bg.CornerRadius = 8
	bar := canvas.NewRectangle(c)
	bar.SetMinSize(fyne.NewSize(6, 6))

	inner := container.NewVBox(layout.NewSpacer(), iconBox, num, t, layout.NewSpacer())
	inner = container.NewPadded(inner)
	content := container.NewBorder(bar, nil, nil, nil, inner)
	return container.NewStack(bg, content)
}

func (d *DashboardScreen) Refresh() {
	products, err := d.svc.ListProducts()
	if err != nil {
		return
	}
	parts, err := d.svc.ListParts()
	if err != nil {
		return
	}
	batches, err := d.svc.ListBatches()
	if err != nil {
		return
	}
	logs, err := d.svc.ListRecentAuditLogs(30)
	if err != nil {
		return
	}

	// 统计
	warnCount := 0
	var warns []model.Part
	for _, p := range parts {
		if isLowStock(p) {
			warnCount++
			warns = append(warns, p)
		}
	}
	sort.Slice(warns, func(i, j int) bool { return warns[i].Code < warns[j].Code })

	activeBatches := 0
	finished := 0
	planned := 0
	for _, b := range batches {
		switch b.Status {
		case 1:
			activeBatches++
		case 2:
			finished++
		case 0:
			planned++
		}
	}

	d.warnData = warns
	d.logData = logs
	if d.lastRefresh != nil {
		d.lastRefresh.SetText("最近刷新: " + time.Now().Format("15:04:05"))
	}

	if d.cardGrid != nil {
		d.cardGrid.Objects = []fyne.CanvasObject{
			statCard("产品总数", len(products), clrPrimary, theme.ComputerIcon()),
			statCard("零件总数", len(parts), clrSuccess, theme.StorageIcon()),
			statCard("库存预警", warnCount, clrError, theme.WarningIcon()),
			statCard("进行中批次", activeBatches, clrWarning, theme.MediaPlayIcon()),
		}
		d.cardGrid.Refresh()
	}
	if d.warnList != nil {
		d.warnList.Refresh()
	}
	if d.logTable != nil {
		d.logTable.Refresh()
	}

	_ = planned
	_ = finished
}
