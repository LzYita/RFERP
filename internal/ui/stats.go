package ui

import (
	"fmt"
	"image/color"
	"math"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"app/internal/service"
)

type StatsScreen struct {
	svc     *service.Service
	window  fyne.Window
	daysSel *widget.Select
	body    *fyne.Container
}

func NewStatsScreen(svc *service.Service, w fyne.Window) *StatsScreen {
	return &StatsScreen{svc: svc, window: w}
}

func (s *StatsScreen) Build() fyne.CanvasObject {
	title := canvas.NewText("统计分析", clrForeground)
	title.TextSize = 20
	title.TextStyle = fyne.TextStyle{Bold: true}

	s.daysSel = widget.NewSelect([]string{"近7天", "近30天", "近90天"}, func(_ string) { s.Refresh() })
	s.daysSel.SetSelected("近30天")

	refreshBtn := widget.NewButtonWithIcon("刷新", theme.ViewRefreshIcon(), s.Refresh)

	header := container.NewBorder(nil, nil, nil,
		container.NewHBox(widget.NewLabel("统计范围:"), s.daysSel, refreshBtn),
		title)

	s.body = container.NewMax()
	content := container.NewBorder(header, nil, nil, nil, s.body)
	s.Refresh()
	return content
}

func (s *StatsScreen) Refresh() {
	if s.body == nil {
		return
	}
	days := 30
	switch s.daysSel.Selected {
	case "近7天":
		days = 7
	case "近90天":
		days = 90
	}

	st, err := s.svc.GetStockStats(days)
	if err != nil {
		showError(s.window, "统计查询失败", err)
		s.body.Objects = []fyne.CanvasObject{
			container.NewCenter(widget.NewLabel("统计查询失败，请检查数据库连接")),
		}
		s.body.Refresh()
		return
	}

	// 汇总卡片
	netChange := st.TotalIn - st.TotalOut
	netColor := clrSuccess
	netIcon := theme.MoveUpIcon()
	if netChange < 0 {
		netColor = clrError
		netIcon = theme.MoveDownIcon()
	}
	cards := container.NewGridWithColumns(4,
		statCardStr("零件入库总量", fmt.Sprintf("%.0f", st.TotalIn), clrSuccess, theme.DownloadIcon()),
		statCardStr("零件出库总量", fmt.Sprintf("%.0f", st.TotalOut), clrWarning, theme.UploadIcon()),
		statCardStr("产品出库总量", fmt.Sprintf("%.0f", st.ProductOutTotal), badgeBlueFg, theme.StorageIcon()),
		statCardStr("净变化", fmt.Sprintf("%+.0f", netChange), netColor, netIcon),
	)

	// 每日进出库趋势折线图
	trend := s.buildTrend(st.Days)

	// 上方两栏：供应商入库 + 客户出库
	topRank := container.NewGridWithColumns(2,
		s.buildRankPanel("供应商入库 Top", rankRowsFromSuppliers(st.TopSuppliers), clrSuccess, "入库"),
		s.buildRankPanel("客户出库 Top", rankRowsFromCustomers(st.TopCustomers), clrWarning, "出库"),
	)

	// 下方两栏：零件入库/出库 Top
	partRank := container.NewGridWithColumns(2,
		s.buildRankPanel("零件入库 Top", rankRowsFromParts(st.TopIn), clrSuccess, "入库"),
		s.buildRankPanel("零件出库 Top", rankRowsFromParts(st.TopOut), clrWarning, "出库"),
	)

	// 产品出库 Top
	prodRank := s.buildRankPanel("产品出库 Top", rankRowsFromProducts(st.TopProducts), badgeBlueFg, "出库")

	main := container.NewVBox(cards, trend, topRank, partRank, prodRank)
	main = container.NewPadded(main)
	scroll := container.NewVScroll(main)

	s.body.Objects = []fyne.CanvasObject{scroll}
	s.body.Refresh()
}

// ---- 数据转换 ----

type rankRow struct {
	name  string
	value float64
}

func rankRowsFromParts(items []service.PartStockStat) []rankRow {
	var out []rankRow
	for _, it := range items {
		name := it.Name
		if name == "" {
			name = it.Code
		}
		out = append(out, rankRow{name: name, value: it.In})
	}
	return out
}

func rankRowsFromSuppliers(items []service.SupplierStockStat) []rankRow {
	var out []rankRow
	for _, it := range items {
		out = append(out, rankRow{name: it.Name, value: it.In})
	}
	return out
}

func rankRowsFromCustomers(items []service.CustomerStockStat) []rankRow {
	var out []rankRow
	for _, it := range items {
		out = append(out, rankRow{name: it.Name, value: it.Out})
	}
	return out
}

func rankRowsFromProducts(items []service.ProductStockStat) []rankRow {
	var out []rankRow
	for _, it := range items {
		name := it.Name
		if name == "" {
			name = it.Code
		}
		out = append(out, rankRow{name: name, value: it.Out})
	}
	return out
}

// ---- 折线图 ----

func (s *StatsScreen) buildTrend(days []service.StockDailyPoint) fyne.CanvasObject {
	width := float32(980)
	height := float32(300)
	padL := float32(56)
	padR := float32(16)
	padT := float32(34)
	padB := float32(34)

	plotW := width - padL - padR
	plotH := height - padT - padB

	maxV := float32(1)
	for _, d := range days {
		if float32(d.In) > maxV {
			maxV = float32(d.In)
		}
		if float32(d.Out) > maxV {
			maxV = float32(d.Out)
		}
	}
	step := float32(math.Ceil(float64(maxV / 4)))
	if step < 1 {
		step = 1
	}
	maxV = step * 4

	objs := []fyne.CanvasObject{}

	// 网格与Y轴刻度
	for i := 0; i <= 4; i++ {
		y := padT + plotH - plotH*float32(i)/4
		line := canvas.NewLine(clrBorder)
		line.Position1 = fyne.NewPos(padL, y)
		line.Position2 = fyne.NewPos(padL+plotW, y)
		line.StrokeWidth = 1
		objs = append(objs, line)
		label := canvas.NewText(fmt.Sprintf("%.0f", float64(step)*float64(i)), clrForeground2)
		label.TextSize = 11
		label.Alignment = fyne.TextAlignTrailing
		label.Move(fyne.NewPos(0, y-8))
		objs = append(objs, label)
	}

	if len(days) > 1 {
		objs = append(objs, plotSeries(days, true, padL, plotW, padT, plotH, maxV, clrSuccess)...)
		objs = append(objs, plotSeries(days, false, padL, plotW, padT, plotH, maxV, clrWarning)...)
	} else {
		objs = append(objs, canvas.NewText("暂无数据", clrForeground2))
	}

	// 底部日期标签（稀疏显示）
	if len(days) > 0 {
		labelEvery := 1
		if len(days) > 14 {
			labelEvery = 2
		}
		for i, d := range days {
			if i%labelEvery != 0 && i != len(days)-1 {
				continue
			}
			x := padL + plotW*float32(i)/float32(len(days)-1)
			lbl := canvas.NewText(d.Date, clrForeground2)
			lbl.TextSize = 10
			lbl.Alignment = fyne.TextAlignCenter
			lbl.Move(fyne.NewPos(x-20, padT+plotH+6))
			objs = append(objs, lbl)
		}
	}

	// 图例放在 header 右侧，不叠加到图上
	legend := container.NewHBox(
		legendDot(clrSuccess, "入库"),
		legendDot(clrWarning, "出库"),
	)

	bg := canvas.NewRectangle(clrSurface)
	bg.CornerRadius = 8
	bg.SetMinSize(fyne.NewSize(width, height))

	plot := container.NewWithoutLayout(objs...)
	plot.Resize(fyne.NewSize(width, height))

	header := container.NewBorder(nil, nil,
		widget.NewLabelWithStyle("每日进出库趋势", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		legend, nil)

	body := container.NewStack(bg, plot)

	return container.NewBorder(header, nil, nil, nil, body)
}

func plotSeries(days []service.StockDailyPoint, isIn bool, padL, plotW, padT, plotH, maxV float32, c color.Color) []fyne.CanvasObject {
	var objs []fyne.CanvasObject
	for i := 1; i < len(days); i++ {
		prev, cur := days[i-1], days[i]
		v1, v2 := prev.Out, cur.Out
		if isIn {
			v1, v2 = prev.In, cur.In
		}
		x1 := padL + plotW*float32(i-1)/float32(len(days)-1)
		x2 := padL + plotW*float32(i)/float32(len(days)-1)
		y1 := padT + plotH - plotH*float32(v1)/maxV
		y2 := padT + plotH - plotH*float32(v2)/maxV

		line := canvas.NewLine(c)
		line.StrokeWidth = 2.5
		line.Position1 = fyne.NewPos(x1, y1)
		line.Position2 = fyne.NewPos(x2, y2)
		objs = append(objs, line)
	}
	return objs
}

func legendDot(c color.Color, text string) fyne.CanvasObject {
	dot := canvas.NewRectangle(c)
	dot.CornerRadius = 5
	dot.SetMinSize(fyne.NewSize(12, 12))
	return container.NewHBox(dot, widget.NewLabel(text))
}

// ---- 横向条形图面板 ----

func (s *StatsScreen) buildRankPanel(title string, rows []rankRow, barColor color.Color, unit string) fyne.CanvasObject {
	maxV := 0.0
	for _, r := range rows {
		if r.value > maxV {
			maxV = r.value
		}
	}
	if maxV <= 0 {
		maxV = 1
	}

	var rowObjs []fyne.CanvasObject
	if len(rows) == 0 {
		rowObjs = append(rowObjs, widget.NewLabel("暂无数据"))
	}
	for _, r := range rows {
		ratio := float32(r.value / maxV)

		nameLbl := widget.NewLabel(r.name)
		nameLbl.Truncation = fyne.TextTruncateEllipsis

		valLbl := widget.NewLabel(fmt.Sprintf("%.0f %s", r.value, unit))
		valLbl.Alignment = fyne.TextAlignTrailing

		bar := canvas.NewRectangle(barColor)
		bar.CornerRadius = 4
		bar.SetMinSize(fyne.NewSize(200*ratio, 18))
		barBox := container.NewHBox(bar, layout.NewSpacer())

		row := container.NewHBox(nameLbl, layout.NewSpacer(), barBox, layout.NewSpacer(), valLbl)
		rowObjs = append(rowObjs, row)
	}

	header := widget.NewLabelWithStyle(title, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	body := container.NewVBox(rowObjs...)

	bg := canvas.NewRectangle(clrSurface)
	bg.CornerRadius = 8

	return container.NewBorder(header, nil, nil, nil,
		container.NewStack(bg, container.NewPadded(body)))
}

// statCardStr 统计卡片（值为字符串，用于显示带符号/小数的数值）
func statCardStr(title, value string, c color.Color, icon fyne.Resource) fyne.CanvasObject {
	num := canvas.NewText(value, clrForeground)
	num.TextSize = 22
	num.Alignment = fyne.TextAlignCenter
	num.TextStyle = fyne.TextStyle{Bold: true}

	t := canvas.NewText(title, clrForeground2)
	t.Alignment = fyne.TextAlignCenter
	t.TextSize = 12

	iconObj := canvas.NewImageFromResource(icon)
	iconObj.SetMinSize(fyne.NewSize(18, 18))
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
