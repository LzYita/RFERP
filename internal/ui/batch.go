package ui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"app/internal/auth"
	"app/internal/model"
	"app/internal/usecase"
)

type BatchScreen struct {
	svc        usecase.Applications
	window     fyne.Window
	data       []model.ProductBatch
	table      *widget.Table
	label      *widget.Label
	traceData  []model.BatchTrace
	traceTable *widget.Table
	selected   int
}

func NewBatchScreen(svc usecase.Applications, w fyne.Window) *BatchScreen {
	return &BatchScreen{svc: svc, window: w}
}

func (s *BatchScreen) Build() fyne.CanvasObject {
	btns := []fyne.CanvasObject{widget.NewButtonWithIcon("刷新", theme.ViewRefreshIcon(), s.Refresh)}
	if auth.CanWrite(auth.ModuleBatch) {
		btns = append(btns,
			withImportance(widget.NewButtonWithIcon("新建批次", theme.ContentAddIcon(), s.createBatch), widget.HighImportance),
			widget.NewButtonWithIcon("开始生产", theme.NavigateNextIcon(), func() { s.setStatus(1) }),
			widget.NewButtonWithIcon("完成生产", theme.ConfirmIcon(), func() { s.setStatus(2) }),
			widget.NewButtonWithIcon("记录投料", theme.UploadIcon(), s.recordTrace),
			widget.NewButtonWithIcon("跳过零件", theme.ContentAddIcon(), s.specialConsume),
			withImportance(widget.NewButtonWithIcon("撤销", theme.DeleteIcon(), s.revokeBatch), widget.DangerImportance),
		)
	}
	topBar := container.NewBorder(nil, nil, nil, container.NewHBox(btns...))

	s.label = widget.NewLabel("共 0 批")

	s.selected = -1
	s.table = widget.NewTable(
		func() (int, int) { return len(s.data) + 1, 7 },
		makeCellTmpl,
		func(tci widget.TableCellID, o fyne.CanvasObject) {
			sel := tci.Row == s.selected && tci.Row > 0
			if tci.Row == 0 {
				headers := []string{"批次号", "产品", "计划数", "完成数", "状态", "客户", "选择"}
				updateCell(o, headers[tci.Col], true, headerColor)
			} else {
				idx := tci.Row - 1
				if idx >= len(s.data) {
					return
				}
				b := s.data[idx]
				bg := dataRowBG(idx, sel)
				var text string
				isBadge := false
				switch tci.Col {
				case 0:
					text = b.BatchNo
				case 1:
					text = fmt.Sprintf("%s [%s]", nullStr(b.ProductName), nullStr(b.ProductCode))
				case 2:
					text = fmt.Sprintf("%d", b.PlanQty)
				case 3:
					text = fmt.Sprintf("%d", b.ProducedQty)
				case 4:
					bbg, _, btext := batchStatusStyle(b.Status)
					bg = bbg
					text = btext
					isBadge = true
				case 5:
					text = nullStr(b.Customer)
				case 6:
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
	s.table.SetColumnWidth(0, 200)
	s.table.SetColumnWidth(1, 200)
	s.table.SetColumnWidth(2, 100)
	s.table.SetColumnWidth(3, 100)
	s.table.SetColumnWidth(4, 110)
	s.table.SetColumnWidth(5, 150)
	s.table.SetColumnWidth(6, 120)

	// 追溯子表
	traceLabel := widget.NewLabelWithStyle("投料追溯记录", fyne.TextAlignCenter, fyne.TextStyle{Bold: true})

	s.traceTable = widget.NewTable(
		func() (int, int) { return len(s.traceData) + 1, 4 },
		makeCellTmpl,
		func(tci widget.TableCellID, o fyne.CanvasObject) {
			if tci.Row == 0 {
				headers := []string{"零件ID", "零件批次", "用量", "供应商"}
				updateCell(o, headers[tci.Col], true, headerColor)
				return
			}
			idx := tci.Row - 1
			if idx >= len(s.traceData) {
				return
			}
			t := s.traceData[idx]
			var text string
			switch tci.Col {
			case 0:
				text = fmt.Sprintf("%d", t.PartID)
			case 1:
				text = nullStr(t.PartBatchNo)
			case 2:
				text = fmt.Sprintf("%.2f", t.UsedQty)
			case 3:
				text = nullStr(t.Supplier)
			}
			updateCell(o, text, false, dataRowBG(idx, false))
		},
	)
	s.traceTable.SetColumnWidth(0, 220)
	s.traceTable.SetColumnWidth(1, 300)
	s.traceTable.SetColumnWidth(2, 180)
	s.traceTable.SetColumnWidth(3, 280)

	// 底部选择批次查看追溯
	bottom := container.NewBorder(nil, nil, nil,
		widget.NewButton("查看选中批次的追溯", func() {
			s.loadTraceForSelected()
		}),
	)

	traceBox := container.NewBorder(traceLabel, bottom, nil, nil, s.traceTable)

	split := container.NewVSplit(
		container.NewBorder(topBar, s.label, nil, nil, s.table),
		traceBox,
	)
	split.Offset = 0.5

	s.Refresh()
	return split
}

func (s *BatchScreen) setStatus(status int) {
	idx := s.selected
	if idx <= 0 || idx-1 >= len(s.data) {
		dialog.ShowInformation("提示", "请先选择一行批次", s.window)
		return
	}
	b := s.data[idx-1]
	if b.Status == 2 || b.Status == 4 {
		dialog.ShowInformation("提示", "已完成的批次不能更改状态", s.window)
		return
	}
	if b.Status == status {
		return
	}
	statusMap := map[int]string{0: "待生产", 1: "生产中", 2: "已完成", 3: "已暂停", 4: "已撤销"}
	dialog.NewConfirm("确认", fmt.Sprintf("确定将批次 %s 标记为【%s】？", b.BatchNo, statusMap[status]), func(ok bool) {
		if !ok {
			return
		}
		if err := s.svc.UpdateBatchStatus(usecase.UpdateBatchStatusInput{ID: b.ID, Status: status, Operator: auth.OperatorName()}); err != nil {
			showError(s.window, "操作失败", err)
			return
		}
		s.Refresh()
	}, s.window).Show()
}

func (s *BatchScreen) Refresh() {
	list, err := s.svc.ListBatches()
	if err != nil {
		showError(s.window, "查询失败", err)
		return
	}
	s.data = list
	s.fitColumns()
	s.label.SetText(fmt.Sprintf("共 %d 批", len(s.data)))
	if s.table != nil {
		s.selected = -1
		s.table.Refresh()
	}
}

func (s *BatchScreen) createBatch() {
	products, err := s.svc.ListProducts()
	if err != nil {
		showError(s.window, "查询产品失败", err)
		return
	}
	if len(products) == 0 {
		dialog.ShowInformation("提示", "请先创建产品", s.window)
		return
	}

	// 收集唯一名称
	nameSet := make(map[string]bool)
	for _, p := range products {
		nameSet[p.Name] = true
	}
	var nameList []string
	for n := range nameSet {
		nameList = append(nameList, n)
	}
	sort.Strings(nameList)

	nameSel := widget.NewSelect(nameList, nil)
	codeSel := widget.NewSelect([]string{}, nil)
	specSel := widget.NewSelect([]string{}, nil)
	codeSel.Disable()
	specSel.Disable()

	nameSel.OnChanged = func(n string) {
		codeSet := make(map[string]bool)
		for _, p := range products {
			if p.Name == n {
				codeSet[p.Code] = true
			}
		}
		var codes []string
		for c := range codeSet {
			codes = append(codes, c)
		}
		sort.Strings(codes)
		codeSel.Options = codes
		codeSel.Selected = ""
		codeSel.Enable()
		specSel.Options = nil
		specSel.Selected = ""
		specSel.Disable()
	}

	codeSel.OnChanged = func(c string) {
		specSet := make(map[string]bool)
		for _, p := range products {
			if p.Name == nameSel.Selected && p.Code == c && p.Spec != nil && *p.Spec != "" {
				specSet[*p.Spec] = true
			}
		}
		var specs []string
		for s := range specSet {
			specs = append(specs, s)
		}
		if len(specs) > 0 {
			sort.Strings(specs)
			specSel.Options = specs
			specSel.Selected = ""
			specSel.Enable()
		} else {
			specSel.Options = nil
			specSel.Selected = ""
			specSel.Disable()
		}
	}

	batchNo := widget.NewEntry()
	batchNo.SetText(time.Now().Format("BATCH20060102150405"))
	planQty := widget.NewEntry()
	planQty.SetText("100")
	customer := widget.NewEntry()
	customer.SetPlaceHolder("客户名称（选填）")

	items := []*widget.FormItem{
		widget.NewFormItem("产品名称", nameSel),
		widget.NewFormItem("产品编码", codeSel),
		widget.NewFormItem("规格型号", specSel),
		widget.NewFormItem("批次号", batchNo),
		widget.NewFormItem("计划数量", planQty),
		widget.NewFormItem("客户", customer),
	}

	d := dialog.NewForm("新建生产批次", "确定", "取消", items, func(ok bool) {
		if !ok {
			return
		}
		if nameSel.Selected == "" {
			dialog.ShowInformation("提示", "请选择产品名称", s.window)
			return
		}
		if codeSel.Selected == "" {
			dialog.ShowInformation("提示", "请选择产品编码", s.window)
			return
		}
		var selectedProductID int64
		for _, p := range products {
			if p.Name == nameSel.Selected && p.Code == codeSel.Selected {
				if specSel.Selected != "" {
					if p.Spec == nil || *p.Spec != specSel.Selected {
						continue
					}
				}
				selectedProductID = p.ID
				break
			}
		}
		if selectedProductID == 0 {
			dialog.ShowInformation("提示", "未找到匹配的产品", s.window)
			return
		}
		qty, err := parseIntText(planQty.Text)
		if err != nil || qty <= 0 {
			dialog.ShowInformation("提示", "计划数量必须是大于0的整数", s.window)
			return
		}
		op := auth.OperatorName()
		c := customer.Text
		b := &model.ProductBatch{
			BatchNo:   batchNo.Text,
			ProductID: selectedProductID,
			PlanQty:   qty,
			Status:    0,
			Operator:  &op,
			Customer:  strPtr(c),
		}
		// 检查BOM零件库存
		bomItems, err := s.svc.GetBOMByProduct(selectedProductID)
		if err != nil {
			showError(s.window, "查询BOM失败", err)
			return
		}
		if len(bomItems) > 0 {
			parts, err := s.svc.ListParts()
			if err != nil {
				showError(s.window, "查询零件失败", err)
				return
			}
			partMap := make(map[int64]model.Part)
			for _, p := range parts {
				partMap[p.ID] = p
			}
			var shortage []string
			for _, bi := range bomItems {
				part, ok := partMap[bi.PartID]
				if !ok {
					continue
				}
				needed := bomConsumeQty(qty, bi)
				if needed > part.StockQty {
					shortage = append(shortage, fmt.Sprintf("%s [%s]: 需要 %.2f，当前 %.2f",
						nullStr(bi.PartName), nullStr(bi.PartCode), needed, part.StockQty))
				}
			}
			if len(shortage) > 0 {
				msg := "以下零件库存不足，无法创建批次：\n"
				for _, s := range shortage {
					msg += "\n" + s
				}
				dialog.ShowInformation("库存不足", msg, s.window)
				return
			}
		}
		_, err = s.svc.CreateBatch(b)
		if err != nil {
			showError(s.window, "创建批次失败", err)
			return
		}
		s.Refresh()
	}, s.window)
	d.Resize(fyne.NewSize(500, 500))
	d.Show()
}

func (s *BatchScreen) getSelectedBatchID() (int64, bool) {
	idx := s.selected
	if idx <= 0 || idx-1 >= len(s.data) {
		dialog.ShowInformation("提示", "请先选择一行批次", s.window)
		return 0, false
	}
	return s.data[idx-1].ID, true
}

func (s *BatchScreen) loadTraceForSelected() {
	batchID, ok := s.getSelectedBatchID()
	if !ok {
		return
	}
	list, err := s.svc.GetTraceByBatch(batchID)
	if err != nil {
		return
	}
	s.traceData = list
	s.traceTable.Refresh()
}

func (s *BatchScreen) recordTrace() {
	idx := s.selected
	if idx <= 0 || idx-1 >= len(s.data) {
		dialog.ShowInformation("提示", "请先选择一行批次", s.window)
		return
	}
	b := s.data[idx-1]

	bomItems, err := s.svc.GetBOMByProduct(b.ProductID)
	if err != nil {
		showError(s.window, "查询BOM失败", err)
		return
	}
	if len(bomItems) == 0 {
		dialog.ShowInformation("提示", "该产品没有BOM明细，请先在BOM管理中配置零件", s.window)
		return
	}

	type traceRow struct {
		qtyEntry *widget.Entry
		batEntry *widget.Entry
		supEntry *widget.Entry
	}
	var rows []traceRow
	formItems := []*widget.FormItem{}

	for _, bi := range bomItems {
		label := fmt.Sprintf("%s [%s]", nullStr(bi.PartName), nullStr(bi.PartCode))
		qty := widget.NewEntry()
		qty.SetText(fmt.Sprintf("%.0f", bomConsumeQty(b.PlanQty, bi)))
		bat := widget.NewEntry()
		bat.SetPlaceHolder("批次号")
		sup := widget.NewEntry()
		sup.SetPlaceHolder("供应商")

		row := container.NewGridWithColumns(3, qty, bat, sup)
		rows = append(rows, traceRow{qty, bat, sup})
		formItems = append(formItems, &widget.FormItem{Text: label, Widget: row})
	}

	d := dialog.NewForm(fmt.Sprintf("记录投料 - %s", b.BatchNo),
		"确定", "取消", formItems, func(ok bool) {
			if !ok {
				return
			}
			op := auth.OperatorName()
			traces := make([]*model.BatchTrace, 0, len(bomItems))
			for i, bi := range bomItems {
				r := rows[i]
				text := strings.TrimSpace(r.qtyEntry.Text)
				if text == "" {
					continue
				}
				qty, err := parseFloatText(text)
				if err != nil || qty <= 0 {
					dialog.ShowInformation("提示", "投料数量必须是大于0的有限数值", s.window)
					return
				}
				pb := r.batEntry.Text
				sup := r.supEntry.Text
				t := &model.BatchTrace{
					BatchID:     b.ID,
					PartID:      bi.PartID,
					PartBatchNo: &pb,
					UsedQty:     qty,
					Supplier:    &sup,
					Operator:    &op,
				}
				traces = append(traces, t)
			}
			if err := s.svc.RecordTraces(traces); err != nil {
				showError(s.window, "批量记录投料失败", err)
				return
			}
			saved := len(traces)
			if saved > 0 {
				s.loadTraceForSelected()
				dialog.ShowInformation("记录完成", fmt.Sprintf("成功记录 %d 个零件的投料信息", saved), s.window)
			}
		}, s.window)
	d.Resize(fyne.NewSize(600, 400))
	d.Show()
}

func (s *BatchScreen) revokeBatch() {
	idx := s.selected
	if idx <= 0 || idx-1 >= len(s.data) {
		dialog.ShowInformation("提示", "请先选择一行批次", s.window)
		return
	}
	b := s.data[idx-1]

	statusMap := map[int]string{0: "待生产", 1: "生产中", 2: "已完成", 3: "已暂停", 4: "已撤销"}
	msg := fmt.Sprintf("确定撤销批次 %s（当前状态：%s）？\n\n", b.BatchNo, statusMap[b.Status])
	if b.Status == 2 {
		msg += "该批次已完成生产，撤销后将回退已扣除的零件库存。"
	} else {
		msg += "撤销后状态将标记为「已撤销」，数据保留。"
	}

	dialog.NewConfirm("确认撤销", msg, func(ok bool) {
		if !ok {
			return
		}
		if err := s.svc.RevokeBatch(usecase.RevokeBatchInput{ID: b.ID, Operator: auth.OperatorName()}); err != nil {
			showError(s.window, "撤销失败", err)
			return
		}
		s.Refresh()
		dialog.ShowInformation("撤销成功", fmt.Sprintf("批次 %s 已撤销", b.BatchNo), s.window)
	}, s.window).Show()
}

func (s *BatchScreen) specialConsume() {
	idx := s.selected
	if idx <= 0 || idx-1 >= len(s.data) {
		dialog.ShowInformation("提示", "请先选择一行批次", s.window)
		return
	}
	b := s.data[idx-1]

	if b.Status == 2 {
		dialog.ShowInformation("提示", "已完成的批次不能跳过零件", s.window)
		return
	}

	bomItems, err := s.svc.GetBOMByProduct(b.ProductID)
	if err != nil {
		showError(s.window, "查询BOM失败", err)
		return
	}
	if len(bomItems) == 0 {
		dialog.ShowInformation("提示", "该产品没有BOM明细，无需跳过", s.window)
		return
	}

	skipped, _ := s.svc.GetSkippedParts(b.ID)
	skipMap := make(map[int64]bool)
	for _, pid := range skipped {
		skipMap[pid] = true
	}

	type checkRow struct {
		partID int64
		check  *widget.Check
	}
	var rows []checkRow

	for _, bi := range bomItems {
		if bi.Replaceable != 1 {
			continue
		}
		label := fmt.Sprintf("%s [%s]", nullStr(bi.PartName), nullStr(bi.PartCode))
		chk := widget.NewCheck(label, nil)
		chk.SetChecked(skipMap[bi.PartID])
		rows = append(rows, checkRow{bi.PartID, chk})
	}

	if len(rows) == 0 {
		dialog.ShowInformation("提示", "该产品的BOM中没有标记为「可替换」的零件\n请在BOM管理中设置", s.window)
		return
	}

	formItems := []*widget.FormItem{
		{Text: "选择要跳过的零件", Widget: widget.NewLabel("勾选后，完成生产时将不扣除该零件库存")},
	}
	for _, r := range rows {
		formItems = append(formItems, &widget.FormItem{Text: "", Widget: r.check})
	}

	d := dialog.NewForm(fmt.Sprintf("跳过零件 - %s", b.BatchNo),
		"保存", "取消", formItems, func(ok bool) {
			if !ok {
				return
			}
			saved := 0
			removed := 0
			for _, r := range rows {
				if r.check.Checked {
					if err := s.svc.AddSkipPart(b.ID, r.partID); err == nil {
						saved++
					}
				} else {
					if err := s.svc.RemoveSkipPart(b.ID, r.partID); err == nil {
						removed++
					}
				}
			}
			dialog.ShowInformation("保存完成",
				fmt.Sprintf("已跳过 %d 个零件，取消跳过 %d 个零件\n完成生产时将不会扣除跳过零件的库存", saved, removed), s.window)
		}, s.window)
	d.Resize(fyne.NewSize(500, 400))
	d.Show()
}
