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

type BOMScreen struct {
	svc      *service.Service
	window   fyne.Window
	nameSel  *widget.Select
	codeSel  *widget.Select
	specSel  *widget.Select
	parts    []model.Part
	bomData  []model.BOMItem
	table    *widget.Table
	label    *widget.Label
	products []model.Product
	selected int
	addBtn   *widget.Button
	delBtn   *widget.Button
}

func NewBOMScreen(svc *service.Service, w fyne.Window) *BOMScreen {
	return &BOMScreen{svc: svc, window: w}
}

func (s *BOMScreen) Build() fyne.CanvasObject {
	s.nameSel = widget.NewSelect([]string{}, nil)
	s.nameSel.PlaceHolder = "名称"
	s.codeSel = widget.NewSelect([]string{}, nil)
	s.codeSel.PlaceHolder = "编码"
	s.codeSel.Disable()
	s.specSel = widget.NewSelect([]string{}, nil)
	s.specSel.PlaceHolder = "规格"
	s.specSel.Disable()

	s.loadProducts()
	s.nameSel.OnChanged = func(n string) {
		codeSet := make(map[string]bool)
		for _, p := range s.products {
			if p.Name == n {
				codeSet[p.Code] = true
			}
		}
		var codes []string
		for c := range codeSet {
			codes = append(codes, c)
		}
		sort.Strings(codes)
		s.codeSel.Options = codes
		s.codeSel.Selected = ""
		s.codeSel.Enable()
		s.specSel.Options = nil
		s.specSel.Selected = ""
		s.specSel.Disable()
		s.onProductChanged()
	}
	s.codeSel.OnChanged = func(c string) {
		specSet := make(map[string]bool)
		for _, p := range s.products {
			if p.Name == s.nameSel.Selected && p.Code == c && p.Spec != nil && *p.Spec != "" {
				specSet[*p.Spec] = true
			}
		}
		var specs []string
		for s := range specSet {
			specs = append(specs, s)
		}
		if len(specs) > 0 {
			sort.Strings(specs)
			s.specSel.Options = specs
			s.specSel.Selected = ""
			s.specSel.Enable()
		} else {
			s.specSel.Options = nil
			s.specSel.Selected = ""
			s.specSel.Disable()
		}
		s.onProductChanged()
	}
	s.specSel.OnChanged = func(_ string) {
		s.onProductChanged()
	}

	selRow := container.NewGridWithColumns(3,
		container.NewBorder(nil, nil, nil, nil, s.nameSel),
		container.NewBorder(nil, nil, nil, nil, s.codeSel),
		container.NewBorder(nil, nil, nil, nil, s.specSel),
	)
	s.addBtn = withImportance(widget.NewButtonWithIcon("添加零件", theme.ContentAddIcon(), s.addPart), widget.HighImportance)
	s.delBtn = withImportance(widget.NewButtonWithIcon("删除", theme.DeleteIcon(), s.removePart), widget.DangerImportance)
	s.addBtn.Disable()
	s.delBtn.Disable()
	btnRow := container.NewHBox(
		widget.NewButtonWithIcon("刷新", theme.ViewRefreshIcon(), s.Refresh),
		s.addBtn, s.delBtn,
	)
	topBar := container.NewBorder(nil, nil,
		widget.NewLabel("选择产品: "), nil,
		container.NewVBox(selRow, btnRow),
	)

	s.label = widget.NewLabel("请先选择产品")
	s.selected = -1
	s.table = widget.NewTable(
		func() (int, int) { return len(s.bomData) + 1, 8 },
		makeCellTmpl,
		func(tci widget.TableCellID, o fyne.CanvasObject) {
			sel := tci.Row == s.selected && tci.Row > 0
			if tci.Row == 0 {
				headers := []string{"零件编码", "零件名称", "用量模式", "用量", "损耗率(%)", "可替换", "备注", "选择"}
				updateCell(o, headers[tci.Col], true, headerColor)
			} else {
				idx := tci.Row - 1
				if idx >= len(s.bomData) {
					return
				}
				b := s.bomData[idx]
				bg := dataRowBG(idx, sel)
				var text string
				isBadge := false
				switch tci.Col {
				case 0:
					text = nullStr(b.PartCode)
				case 1:
					text = nullStr(b.PartName)
				case 2:
					if b.UseMode == 1 {
						text = "每M台用1个"
						bg = badgeBlueBg
						isBadge = true
					} else {
						text = "每台用N个"
						bg = badgeGrayBg
						isBadge = true
					}
				case 3:
					if b.UseMode == 1 {
						text = fmt.Sprintf("%.0f台", b.Quantity)
					} else {
						text = fmt.Sprintf("%.2f个", b.Quantity)
					}
				case 4:
					text = fmt.Sprintf("%.1f", b.LossRate)
				case 5:
					if b.Replaceable == 1 {
						text = "可替换"
						bg = badgeWarnBg
						isBadge = true
					} else {
						text = "—"
					}
				case 6:
					text = nullStr(b.Remark)
				case 7:
					if sel {
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
	s.table.SetColumnWidth(0, 130)
	s.table.SetColumnWidth(1, 200)
	s.table.SetColumnWidth(2, 130)
	s.table.SetColumnWidth(3, 110)
	s.table.SetColumnWidth(4, 110)
	s.table.SetColumnWidth(5, 100)
	s.table.SetColumnWidth(6, 140)
	s.table.SetColumnWidth(7, 60)

	s.loadProducts()
	return container.NewBorder(topBar, s.label, nil, nil, s.table)
}

func (s *BOMScreen) loadProducts() {
	list, err := s.svc.ListProducts()
	if err != nil {
		return
	}
	s.products = list
	nameSet := make(map[string]bool)
	for _, p := range list {
		nameSet[p.Name] = true
	}
	var names []string
	for n := range nameSet {
		names = append(names, n)
	}
	sort.Strings(names)
	if s.nameSel != nil {
		s.nameSel.Options = names
	}
}

func (s *BOMScreen) onProductChanged() {
	ready := false
	if s.nameSel.Selected != "" && s.codeSel.Selected != "" {
		if s.specSel.Disabled() || s.specSel.Selected != "" {
			ready = true
		}
	}
	if !ready {
		s.bomData = nil
		s.label.SetText("请先选择完整产品信息（名称+编码）")
		if s.table != nil {
			s.table.Refresh()
		}
		s.addBtn.Disable()
		s.delBtn.Disable()
		return
	}
	for _, p := range s.products {
		if p.Name == s.nameSel.Selected && p.Code == s.codeSel.Selected {
			if !s.specSel.Disabled() && s.specSel.Selected != "" {
				if p.Spec == nil || *p.Spec != s.specSel.Selected {
					continue
				}
			}
			if s.specSel.Disabled() && p.Spec != nil && *p.Spec != "" {
				continue
			}
			s.loadBOM(p.ID)
			s.addBtn.Enable()
			s.delBtn.Enable()
			return
		}
	}
}

func (s *BOMScreen) loadBOM(productID int64) {
	list, err := s.svc.GetBOMByProduct(productID)
	if err != nil {
		showError(s.window, "查询BOM失败", err)
		return
	}
	s.bomData = list
	s.label.SetText(fmt.Sprintf("共 %d 个零件", len(list)))
	if s.table != nil {
		s.selected = -1
		s.table.Refresh()
	}
}

func (s *BOMScreen) Refresh() {
	s.loadProducts()
	s.bomData = nil
	s.label.SetText("请先选择产品")
	if s.nameSel != nil {
		s.nameSel.Selected = ""
		s.codeSel.Options = nil
		s.codeSel.Selected = ""
		s.codeSel.Disable()
		s.specSel.Options = nil
		s.specSel.Selected = ""
		s.specSel.Disable()
	}
	if s.table != nil {
		s.table.Refresh()
	}
}

func (s *BOMScreen) getSelectedProductID() (int64, bool) {
	if s.nameSel.Selected == "" || s.codeSel.Selected == "" {
		dialog.ShowInformation("提示", "请先选择产品（名称+编码）", s.window)
		return 0, false
	}
	for _, p := range s.products {
		if p.Name == s.nameSel.Selected && p.Code == s.codeSel.Selected {
			if s.specSel.Selected != "" {
				if p.Spec == nil || *p.Spec != s.specSel.Selected {
					continue
				}
			}
			return p.ID, true
		}
	}
	dialog.ShowInformation("提示", "未找到匹配的产品", s.window)
	return 0, false
}

func (s *BOMScreen) addPart() {
	pid, ok := s.getSelectedProductID()
	if !ok {
		return
	}

	partCode := widget.NewEntry()
	partCode.SetPlaceHolder("输入零件编码")

	modeSel := widget.NewSelect([]string{"每台用N个零件", "每M台用1个零件（如包装箱）"}, nil)
	modeSel.SetSelected("每台用N个零件")
	qtyLabel := widget.NewLabel("每台用量(个)")
	qty := widget.NewEntry()
	qty.SetText("1")
	loss := widget.NewEntry()
	loss.SetText("0")
	replaceable := widget.NewCheck("该零件可被替换（跳过时不消耗库存）", nil)
	remark := widget.NewEntry()

	modeSel.OnChanged = func(m string) {
		if m == "每M台用1个零件（如包装箱）" {
			qtyLabel.SetText("每多少台用1个(M)")
			qty.SetText("1")
		} else {
			qtyLabel.SetText("每台用量(个)")
			qty.SetText("1")
		}
	}

	items := []*widget.FormItem{
		widget.NewFormItem("零件编码", partCode),
		widget.NewFormItem("用量模式", modeSel),
		{Text: "", Widget: qtyLabel},
		{Text: "", Widget: qty},
		widget.NewFormItem("损耗率%", loss),
		{Text: "", Widget: replaceable},
		widget.NewFormItem("备注", remark),
	}

	d := dialog.NewForm("添加BOM零件", "确定", "取消", items, func(ok bool) {
		if !ok {
			return
		}
		// 根据编码查找零件
		parts, err := s.svc.ListParts()
		if err != nil {
			showError(s.window, "查询零件失败", err)
			return
		}
		var found *model.Part
		for _, p := range parts {
			if p.Code == partCode.Text {
				found = &p
				break
			}
		}
		if found == nil {
			dialog.ShowInformation("提示", fmt.Sprintf("未找到零件编码: %s", partCode.Text), s.window)
			return
		}

		q := 1.0
		l := 0.0
		_, _ = fmt.Sscanf(qty.Text, "%f", &q)
		_, _ = fmt.Sscanf(loss.Text, "%f", &l)
		op := "admin"
		r := remark.Text
		rep := 0
		if replaceable.Checked {
			rep = 1
		}
		useMode := 0
		if modeSel.Selected == "每M台用1个零件（如包装箱）" {
			useMode = 1
			if q <= 0 {
				dialog.ShowInformation("提示", "每多少台用1个(M)必须大于0", s.window)
				return
			}
		}
		b := &model.BOMItem{
			ProductID:   pid,
			PartID:      found.ID,
			Quantity:    q,
			LossRate:    l,
			Remark:      &r,
			Operator:    &op,
			Replaceable: rep,
			UseMode:     useMode,
		}
		_, err = s.svc.AddBOMItem(b)
		if err != nil {
			showError(s.window, "添加BOM失败", err)
			return
		}
		s.loadBOM(pid)
	}, s.window)
	d.Resize(fyne.NewSize(500, 0))
	d.Show()
}

func (s *BOMScreen) removePart() {
	pid, ok := s.getSelectedProductID()
	if !ok {
		return
	}
	idx := s.selected
	if idx <= 0 || idx-1 >= len(s.bomData) {
		dialog.ShowInformation("提示", "请先选择一行", s.window)
		return
	}
	b := s.bomData[idx-1]
	dialog.NewConfirm("确认删除", "确定从BOM中移除该零件？", func(ok bool) {
		if !ok {
			return
		}
		if err := s.svc.RemoveBOMItem(b.ID, "admin"); err != nil {
			showError(s.window, "删除失败", err)
			return
		}
		s.loadBOM(pid)
	}, s.window).Show()
}
