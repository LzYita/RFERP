package ui

import (
	"fmt"
	"sort"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"app/internal/model"
	"app/internal/service"
)

type PartScreen struct {
	svc        *service.Service
	window     fyne.Window
	data       []model.Part
	table      *widget.Table
	label      *widget.Label
	selected   int
	filterType string
	filterSel  *widget.Select
}

func NewPartScreen(svc *service.Service, w fyne.Window) *PartScreen {
	return &PartScreen{svc: svc, window: w}
}

func isLowStock(p model.Part) bool {
	return p.WarnQty > 0 && p.StockQty < p.WarnQty
}

func (s *PartScreen) Build() fyne.CanvasObject {
	s.filterType = "全部"
	s.filterSel = widget.NewSelect([]string{}, func(t string) {
		s.filterType = t
		s.Refresh()
	})
	s.filterSel.PlaceHolder = "全部"

	topBar := container.NewBorder(nil, nil, nil,
		container.NewHBox(
			widget.NewButtonWithIcon("刷新", theme.ViewRefreshIcon(), s.Refresh),
			withImportance(widget.NewButtonWithIcon("新增", theme.ContentAddIcon(), s.add), widget.HighImportance),
			widget.NewButtonWithIcon("编辑", theme.DocumentCreateIcon(), s.edit),
			widget.NewButton("入库", s.stockIn),
			widget.NewButtonWithIcon("盘点", theme.InfoIcon(), s.adjustStock),
			widget.NewButtonWithIcon("预警列表", theme.WarningIcon(), s.showWarnList),
			withImportance(widget.NewButtonWithIcon("删除", theme.DeleteIcon(), s.delete), widget.DangerImportance),
			widget.NewSeparator(),
			widget.NewLabel("分类:"),
			s.filterSel,
		),
	)

	s.label = widget.NewLabel("共 0 条记录")

	s.selected = -1
	s.table = widget.NewTable(
		func() (int, int) { return len(s.data) + 1, 9 },
		makeCellTmpl,
		func(tci widget.TableCellID, o fyne.CanvasObject) {
			sel := tci.Row == s.selected && tci.Row > 0
			if tci.Row == 0 {
				headers := []string{"编码", "名称", "规格", "分类", "库存", "预警库存", "状态", "供应商", "选择"}
				updateCell(o, headers[tci.Col], true, headerColor)
			} else {
				idx := tci.Row - 1
				if idx >= len(s.data) {
					return
				}
				p := s.data[idx]
				warn := isLowStock(p)
				bg := dataRowBG(idx, sel)
				if warn && !sel {
					bg = warnColor
				}
				var text string
				isBadge := false
				switch tci.Col {
				case 0:
					text = p.Code
				case 1:
					text = p.Name
				case 2:
					text = nullStr(p.Spec)
				case 3:
					text = nullStr(p.PartType)
				case 4:
					text = fmt.Sprintf("%.2f", p.StockQty)
				case 5:
					text = fmt.Sprintf("%.2f", p.WarnQty)
				case 6:
					bbg, _, btext := partStatusStyle(warn, p.Status)
					bg = bbg
					text = btext
					isBadge = true
				case 7:
					text = nullStr(p.Supplier)
				case 8:
					if tci.Row == s.selected {
						text = "☑"
					} else {
						text = "☐"
					}
				}
				updateCellEx(o, text, true, bg, isBadge)
			}
		},
	)
	var selGuard bool
	s.table.OnSelected = func(id widget.TableCellID) {
		if selGuard {
			selGuard = false
			return
		}
		if id.Row == 0 {
			return
		}
		if s.selected == id.Row {
			s.selected = -1
		} else {
			s.selected = id.Row
		}
		s.table.Refresh()
		selGuard = true
		s.table.Select(widget.TableCellID{Row: -1, Col: -1})
	}
	s.table.SetColumnWidth(0, 120)
	s.table.SetColumnWidth(1, 180)
	s.table.SetColumnWidth(2, 140)
	s.table.SetColumnWidth(3, 90)
	s.table.SetColumnWidth(4, 90)
	s.table.SetColumnWidth(5, 90)
	s.table.SetColumnWidth(6, 100)
	s.table.SetColumnWidth(7, 110)
	s.table.SetColumnWidth(8, 60)

	s.Refresh()
	return container.NewBorder(topBar, s.label, nil, nil, s.table)
}

func (s *PartScreen) showWarnList() {
	var warnParts []model.Part
	for _, p := range s.data {
		if isLowStock(p) {
			warnParts = append(warnParts, p)
		}
	}
	if len(warnParts) == 0 {
		dialog.ShowInformation("预警列表", "当前没有库存预警的零件", s.window)
		return
	}

	w := fyne.CurrentApp().NewWindow(fmt.Sprintf("库存预警 - %d 个零件", len(warnParts)))

	table := widget.NewTable(
		func() (int, int) { return len(warnParts) + 1, 5 },
		makeCellTmpl,
		func(tci widget.TableCellID, o fyne.CanvasObject) {
			if tci.Row == 0 {
				headers := []string{"编码", "名称", "库存", "预警库存", "供应商"}
				updateCell(o, headers[tci.Col], true, transparent)
			} else {
				idx := tci.Row - 1
				if idx >= len(warnParts) {
					return
				}
				p := warnParts[idx]
				var text string
				switch tci.Col {
				case 0:
					text = p.Code
				case 1:
					text = p.Name
				case 2:
					text = fmt.Sprintf("%.2f", p.StockQty)
				case 3:
					text = fmt.Sprintf("%.2f", p.WarnQty)
				case 4:
					text = nullStr(p.Supplier)
				}
				updateCell(o, text, false, warnColor)
			}
		},
	)
	table.SetColumnWidth(0, 120)
	table.SetColumnWidth(1, 200)
	table.SetColumnWidth(2, 80)
	table.SetColumnWidth(3, 80)
	table.SetColumnWidth(4, 120)

	w.SetContent(container.NewBorder(
		widget.NewLabelWithStyle(fmt.Sprintf("共 %d 个零件库存低于预警线", len(warnParts)), fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
		nil, nil, nil, table,
	))
	w.Resize(fyne.NewSize(600, 400))
	w.CenterOnScreen()
	w.Show()
}

func (s *PartScreen) Refresh() {
	list, err := s.svc.ListParts()
	if err != nil {
		showError(s.window, "查询失败", err)
		return
	}
	// 收集所有分类
	typeSet := make(map[string]bool)
	for _, p := range list {
		if p.PartType != nil && *p.PartType != "" {
			typeSet[*p.PartType] = true
		}
	}
	typeList := []string{"全部"}
	for t := range typeSet {
		typeList = append(typeList, t)
	}
	sort.Strings(typeList[1:])
	if s.filterSel != nil {
		s.filterSel.Options = typeList
		if s.filterType != "全部" {
			found := false
			for _, t := range typeList {
				if t == s.filterType {
					found = true
					break
				}
			}
			if !found {
				s.filterType = "全部"
			}
		}
		s.filterSel.Selected = s.filterType
		s.filterSel.Refresh()
	}
	// 应用筛选
	if s.filterType != "全部" {
		var filtered []model.Part
		for _, p := range list {
			if p.PartType != nil && *p.PartType == s.filterType {
				filtered = append(filtered, p)
			}
		}
		list = filtered
	}
	sort.Slice(list, func(i, j int) bool {
		wi := isLowStock(list[i])
		wj := isLowStock(list[j])
		if wi != wj {
			return wi
		}
		return list[i].Code < list[j].Code
	})
	s.data = list
	s.label.SetText(fmt.Sprintf("共 %d 条记录", len(s.data)))
	if s.table != nil {
		s.selected = -1
		s.table.Refresh()
	}
}

func (s *PartScreen) add() {
	code := widget.NewEntry()
	code.SetPlaceHolder("零件编码")
	name := widget.NewEntry()
	name.SetPlaceHolder("零件名称")
	spec := widget.NewEntry()
	spec.SetPlaceHolder("规格型号")
	partType := widget.NewSelectEntry([]string{"塑料", "五金", "硅胶", "包装辅料", "其他"})
	partType.SetText("电子")
	stock := widget.NewEntry()
	stock.SetText("0")
	warnQty := widget.NewEntry()
	warnQty.SetText("0")
	warnQty.SetPlaceHolder("0=不预警")
	supplier := widget.NewEntry()
	supplier.SetPlaceHolder("供应商名称（选填）")

	items := []*widget.FormItem{
		widget.NewFormItem("编码", code),
		widget.NewFormItem("名称", name),
		widget.NewFormItem("规格", spec),
		widget.NewFormItem("分类", partType),
		widget.NewFormItem("库存", stock),
		widget.NewFormItem("预警库存", warnQty),
		widget.NewFormItem("供应商", supplier),
	}

	d := dialog.NewForm("新增零件", "确定", "取消", items, func(ok bool) {
		if !ok {
			return
		}
		op := "admin"
		qty := 0.0
		_, _ = fmt.Sscanf(stock.Text, "%f", &qty)
		warn := 0.0
		_, _ = fmt.Sscanf(warnQty.Text, "%f", &warn)
		sup := supplier.Text
		p := &model.Part{
			Code:     code.Text,
			Name:     name.Text,
			Spec:     strPtr(spec.Text),
			PartType: strPtr(partType.Text),
			StockQty: qty,
			WarnQty:  warn,
			Status:   1,
			Operator: &op,
			Supplier: strPtr(sup),
		}
		_, err := s.svc.CreatePart(p)
		if err != nil {
			showError(s.window, "新增失败", err)
			return
		}
		s.Refresh()
	}, s.window)
	d.Resize(fyne.NewSize(500, 0))
	d.Show()
}

func (s *PartScreen) edit() {
	idx := s.selected
	if idx <= 0 || idx-1 >= len(s.data) {
		dialog.ShowInformation("提示", "请先选择一行", s.window)
		return
	}
	p := s.data[idx-1]

	code := widget.NewEntry()
	code.SetText(p.Code)
	name := widget.NewEntry()
	name.SetText(p.Name)
	spec := widget.NewEntry()
	spec.SetText(nullStr(p.Spec))
	partType := widget.NewSelectEntry([]string{"塑料", "五金", "硅胶", "包装辅料", "其他"})
	partType.SetText(nullStr(p.PartType))
	warnQty := widget.NewEntry()
	warnQty.SetText(fmt.Sprintf("%.2f", p.WarnQty))
	supplier := widget.NewEntry()
	supplier.SetText(nullStr(p.Supplier))

	items := []*widget.FormItem{
		widget.NewFormItem("编码", code),
		widget.NewFormItem("名称", name),
		widget.NewFormItem("规格", spec),
		widget.NewFormItem("分类", partType),
		widget.NewFormItem("预警库存", warnQty),
		widget.NewFormItem("供应商", supplier),
	}

	d := dialog.NewForm("编辑零件", "确定", "取消", items, func(ok bool) {
		if !ok {
			return
		}
		op := "admin"
		warn := 0.0
		_, _ = fmt.Sscanf(warnQty.Text, "%f", &warn)
		sup := supplier.Text
		up := &model.Part{
			ID:       p.ID,
			Code:     code.Text,
			Name:     name.Text,
			Spec:     strPtr(spec.Text),
			PartType: strPtr(partType.Text),
			StockQty: p.StockQty,
			WarnQty:  warn,
			Status:   p.Status,
			Version:  p.Version,
			Operator: &op,
			Supplier: strPtr(sup),
		}
		_, err := s.svc.UpdatePart(up)
		if err != nil {
			showError(s.window, "编辑失败", err)
			return
		}
		s.Refresh()
	}, s.window)
	d.Resize(fyne.NewSize(500, 0))
	d.Show()
}

func (s *PartScreen) delete() {
	idx := s.selected
	if idx <= 0 || idx-1 >= len(s.data) {
		dialog.ShowInformation("提示", "请先选择一行", s.window)
		return
	}
	p := s.data[idx-1]
	dialog.NewConfirm("确认删除", fmt.Sprintf("确定删除零件 %s(%s)？", p.Name, p.Code), func(ok bool) {
		if !ok {
			return
		}
		if err := s.svc.DeletePart(p.ID, "admin"); err != nil {
			showError(s.window, "删除失败", err)
			return
		}
		s.Refresh()
	}, s.window).Show()
}

func (s *PartScreen) stockIn() {
	idx := s.selected
	if idx <= 0 || idx-1 >= len(s.data) {
		dialog.ShowInformation("提示", "请先选择一行", s.window)
		return
	}
	p := s.data[idx-1]

	qty := widget.NewEntry()
	qty.SetPlaceHolder("入库数量")

	dialog.NewForm("入库", "确定", "取消", []*widget.FormItem{
		{Text: fmt.Sprintf("零件: %s [%s]", p.Name, p.Code), Widget: widget.NewLabel(fmt.Sprintf("当前库存: %.2f", p.StockQty))},
		{Text: "入库数量", Widget: qty},
	}, func(ok bool) {
		if !ok {
			return
		}
		v := 0.0
		_, _ = fmt.Sscanf(qty.Text, "%f", &v)
		if v <= 0 {
			dialog.ShowInformation("提示", "数量必须大于0", s.window)
			return
		}
		if err := s.svc.StockIn(p.ID, v, "admin"); err != nil {
			showError(s.window, "入库失败", err)
			return
		}
		s.Refresh()
	}, s.window).Show()
}

func (s *PartScreen) adjustStock() {
	idx := s.selected
	if idx <= 0 || idx-1 >= len(s.data) {
		dialog.ShowInformation("提示", "请先选择一行", s.window)
		return
	}
	p := s.data[idx-1]

	qty := widget.NewEntry()
	qty.SetText(fmt.Sprintf("%.2f", p.StockQty))

	dialog.NewForm("盘点调整", "确定", "取消", []*widget.FormItem{
		{Text: fmt.Sprintf("零件: %s [%s]", p.Name, p.Code), Widget: widget.NewLabel(fmt.Sprintf("当前库存: %.2f", p.StockQty))},
		{Text: "调整后数量", Widget: qty},
	}, func(ok bool) {
		if !ok {
			return
		}
		v := 0.0
		_, _ = fmt.Sscanf(qty.Text, "%f", &v)
		if v < 0 {
			dialog.ShowInformation("提示", "数量不能为负", s.window)
			return
		}
		if err := s.svc.AdjustStock(p.ID, v, "admin"); err != nil {
			showError(s.window, "盘点失败", err)
			return
		}
		s.Refresh()
	}, s.window).Show()
}
