package ui

import (
	"encoding/json"
	"fmt"
	"image/color"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"app/internal/model"
	"app/internal/usecase"
)

type AuditScreen struct {
	svc     usecase.Applications
	window  fyne.Window
	allData []model.AuditLog
	data    []model.AuditLog
	table   *widget.Table
	label   *widget.Label
	filter  string
}

func NewAuditScreen(svc usecase.Applications, w fyne.Window) *AuditScreen {
	return &AuditScreen{svc: svc, window: w, filter: "all"}
}

func (s *AuditScreen) Build() fyne.CanvasObject {
	filterBar := container.NewHBox(
		widget.NewButton("全部", func() { s.setFilter("all") }),
		widget.NewButton("产品", func() { s.setFilter("products") }),
		widget.NewButton("BOM", func() { s.setFilter("bom_items") }),
		widget.NewButton("生产批次", func() { s.setFilter("product_batches") }),
		widget.NewButton("批次追溯", func() { s.setFilter("batch_trace") }),
		widget.NewButton("零件流水", func() { s.setFilter("parts") }),
		widget.NewButtonWithIcon("刷新", theme.ViewRefreshIcon(), s.Refresh),
	)

	s.label = widget.NewLabel("共 0 条记录")

	headers := []string{"时间", "类型", "操作", "对象", "变更明细", "操作人"}
	s.table = widget.NewTable(
		func() (int, int) { return len(s.data) + 1, len(headers) },
		makeCellTmpl,
		func(tci widget.TableCellID, o fyne.CanvasObject) {
			if tci.Row == 0 {
				updateCell(o, headers[tci.Col], true, headerColor)
				return
			}
			idx := tci.Row - 1
			if idx >= len(s.data) {
				return
			}
			a := s.data[idx]
			rowBG := dataRowBG(idx, false)
			switch tci.Col {
			case 0:
				updateCell(o, a.CreatedAt.Format("01-02 15:04"), true, rowBG)
			case 1:
				bg, _ := tableTypeStyle(a.TableName)
				updateCellEx(o, tableLabel(a.TableName), true, bg, true)
			case 2:
				bg, _ := actionStyle(a.Action)
				updateCellEx(o, actionLabel(a.Action), true, bg, true)
			case 3:
				updateCell(o, auditObject(a), true, rowBG)
			case 4:
				updateCellWrap(o, changeText(auditChanges(a)), rowBG)
			case 5:
				updateCell(o, nullStr(a.Operator), true, rowBG)
			}
		},
	)
	s.table.SetColumnWidth(0, 110)
	s.table.SetColumnWidth(1, 80)
	s.table.SetColumnWidth(2, 80)
	s.table.SetColumnWidth(3, 200)
	s.table.SetColumnWidth(4, 540)
	s.table.SetColumnWidth(5, 80)
	s.table.OnSelected = func(id widget.TableCellID) {
		if id.Row <= 0 || id.Row-1 >= len(s.data) {
			return
		}
		s.showDetail(s.data[id.Row-1])
	}

	s.Refresh()
	return container.NewBorder(filterBar, s.label, nil, nil, s.table)
}

const changeColWidth = 540

// estimateLines 估算文本在给定像素宽度下的换行行数（中文按 1、英文按 0.55 个字符宽计）。
func estimateLines(text string, width float32) int {
	if text == "" {
		return 1
	}
	const fontSize = 14.0
	perLine := float64(width) / fontSize
	if perLine < 1 {
		perLine = 1
	}
	units := 0.0
	for _, r := range text {
		if r > 127 {
			units++
		} else {
			units += 0.55
		}
	}
	lines := int(units/perLine) + 1
	if lines < 1 {
		lines = 1
	}
	return lines
}

func (s *AuditScreen) refreshRowHeights() {
	if s.table == nil {
		return
	}
	for i, a := range s.data {
		lines := estimateLines(changeText(auditChanges(a)), changeColWidth)
		h := float32(lines)*22 + 12
		if h < 40 {
			h = 40
		}
		s.table.SetRowHeight(i+1, h)
	}
}

func (s *AuditScreen) setFilter(f string) {
	s.filter = f
	s.applyFilter()
}

func (s *AuditScreen) applyFilter() {
	switch s.filter {
	case "products":
		s.data = filterByTable(s.allData, []string{"products"})
	case "bom_items":
		s.data = filterByTable(s.allData, []string{"bom_items"})
	case "product_batches":
		s.data = filterByTable(s.allData, []string{"product_batches"})
	case "batch_trace":
		s.data = filterByTable(s.allData, []string{"batch_trace"})
	case "parts":
		s.data = filterByTable(s.allData, []string{"parts"})
	default:
		s.data = s.allData
	}
	s.label.SetText(fmt.Sprintf("共 %d 条记录", len(s.data)))
	if s.table != nil {
		s.refreshRowHeights()
		s.table.Refresh()
	}
}

func (s *AuditScreen) Refresh() {
	list, err := s.svc.ListRecentAuditLogs(500)
	if err != nil {
		showError(s.window, "查询审计日志失败", err)
		return
	}
	s.allData = list
	s.applyFilter()
}

func filterByTable(data []model.AuditLog, tables []string) []model.AuditLog {
	m := make(map[string]bool, len(tables))
	for _, t := range tables {
		m[t] = true
	}
	var out []model.AuditLog
	for _, a := range data {
		if m[a.TableName] {
			out = append(out, a)
		}
	}
	return out
}

func filterPartStock(data []model.AuditLog) []model.AuditLog {
	var out []model.AuditLog
	for _, a := range data {
		if a.TableName == "parts" {
			out = append(out, a)
		}
	}
	return out
}

func tableLabel(t string) string {
	switch t {
	case "products":
		return "产品"
	case "parts":
		return "零件"
	case "bom_items":
		return "BOM"
	case "product_batches":
		return "批次"
	case "batch_trace":
		return "批次追溯"
	case "batch_skip_parts":
		return "跳过零件"
	default:
		return t
	}
}

func actionLabel(action string) string {
	switch action {
	case "INSERT":
		return "新增"
	case "UPDATE":
		return "修改"
	case "DELETE":
		return "删除"
	case "STOCK_IN":
		return "入库"
	case "STOCK_DEDUCT":
		return "出库"
	case "STOCK_ADJUST":
		return "盘点"
	case "REVOKE":
		return "撤销"
	case "UPDATE_STATUS":
		return "状态变更"
	default:
		return action
	}
}

func shortSummary(a model.AuditLog) string {
	action := a.Action
	table := a.TableName

	// 批次状态变更: 显示批次号 + 产品编码/名称 + 新状态
	if table == "product_batches" && action == "UPDATE_STATUS" {
		batchPart := ""
		cust := ""
		if a.OldData != nil {
			d := *a.OldData
			batchPart = fmt.Sprintf("批次 %v", d["batch_no"])
			if d["product_code"] != nil && fmt.Sprintf("%v", d["product_code"]) != "" {
				batchPart += fmt.Sprintf(" [%v]%v", d["product_code"], d["product_name"])
			}
			if c, ok := d["customer"].(string); ok && c != "" {
				cust = fmt.Sprintf(" 客户:%s", c)
			}
		}
		if a.NewData != nil {
			s := fmt.Sprintf("%v", (*a.NewData)["status"])
			statusMap := map[string]string{"0": "待生产", "1": "生产中", "2": "已完成", "3": "已暂停", "4": "已撤销"}
			if name, ok := statusMap[s]; ok {
				return batchPart + " → " + name + cust
			}
			return batchPart + " → " + s + cust
		}
	}
	// 新建批次: 显示批次号 + 产品编码/名称 + 计划数 + 客户
	if table == "product_batches" && action == "INSERT" && a.NewData != nil {
		d := *a.NewData
		bn := fmt.Sprintf("批次 %v", d["batch_no"])
		prodInfo := ""
		if d["product_code"] != nil && fmt.Sprintf("%v", d["product_code"]) != "" {
			prodInfo = fmt.Sprintf(" [%v]%v", d["product_code"], d["product_name"])
		}
		pq := fmt.Sprintf("计划%v", d["plan_qty"])
		cu := ""
		if d["customer"] != nil && fmt.Sprintf("%v", d["customer"]) != "" {
			cu = fmt.Sprintf("客户:%v", d["customer"])
		}
		return strings.TrimSpace(bn + prodInfo + " " + pq + " " + cu)
	}
	if table == "product_batches" && action == "REVOKE" && a.NewData != nil {
		d := *a.NewData
		bn, _ := d["batch_no"].(string)
		pq, _ := d["plan_qty"].(float64)
		st, _ := d["status"].(float64)
		statusMap := map[string]string{"0": "待生产", "1": "生产中", "2": "已完成", "3": "已暂停", "4": "已撤销"}
		stText := statusMap[fmt.Sprintf("%.0f", st)]
		cust := ""
		if c, ok := d["customer"].(string); ok && c != "" {
			cust = fmt.Sprintf(" 客户:%s", c)
		}
		return fmt.Sprintf("撤销批次 %s [%s 计划%.0f]%s", bn, stText, pq, cust)
	}
	if action == "STOCK_IN" && a.NewData != nil {
		partInfo := partCodeName(a.OldData) + partSupplier(a.OldData)
		return fmt.Sprintf("%s入库 %.0f，当前 %.0f", partInfo, getFloat(*a.NewData, "in_qty"), getFloat(*a.NewData, "new_stock"))
	}
	if action == "STOCK_DEDUCT" && a.NewData != nil {
		partInfo := partCodeName(a.OldData) + partSupplier(a.OldData)
		return fmt.Sprintf("%s生产消耗 %.0f，库存 %.0f→%.0f", partInfo, getFloat(*a.NewData, "deduct"), getFloat(*a.NewData, "old_stock"), getFloat(*a.NewData, "new_stock"))
	}
	if action == "STOCK_ADJUST" && a.NewData != nil {
		partInfo := partCodeName(a.OldData) + partSupplier(a.OldData)
		return fmt.Sprintf("%s%.0f → %.0f", partInfo, getFloat(*a.NewData, "old_stock"), getFloat(*a.NewData, "new_stock"))
	}
	if table == "batch_trace" && action == "INSERT" && a.NewData != nil {
		d := *a.NewData
		pbn := fmt.Sprintf("%v", d["part_batch_no"])
		q := getFloat(d, "used_qty")
		if pbn != "" && pbn != "<nil>" {
			return fmt.Sprintf("记录投料 %s，用量 %.0f", pbn, q)
		}
		return fmt.Sprintf("记录投料，用量 %.0f", q)
	}
	if action == "INSERT" && a.NewData != nil {
		sup := partSupplier(a.NewData)
		if name, ok := (*a.NewData)["name"]; ok {
			return fmt.Sprintf("新增: %v %s", name, sup)
		}
		if code, ok := (*a.NewData)["code"]; ok {
			return fmt.Sprintf("新增: %v %s", code, sup)
		}
	}
	if action == "DELETE" && a.OldData != nil {
		sup := partSupplier(a.OldData)
		if name, ok := (*a.OldData)["name"]; ok {
			return fmt.Sprintf("删除: %v %s", name, sup)
		}
		if code, ok := (*a.OldData)["code"]; ok {
			return fmt.Sprintf("删除: %v %s", code, sup)
		}
	}
	if action == "UPDATE" && a.OldData != nil && a.NewData != nil {
		old := *a.OldData
		new := *a.NewData
		var changes []string
		for _, k := range []string{"code", "name", "spec", "unit", "part_type", "stock_qty", "warn_qty", "supplier", "status"} {
			ov, oExists := old[k]
			nv, nExists := new[k]
			if oExists && nExists && fmt.Sprintf("%v", ov) != fmt.Sprintf("%v", nv) {
				label := k
				switch k {
				case "stock_qty":
					label = "库存"
				case "warn_qty":
					label = "预警"
				case "part_type":
					label = "分类"
				case "supplier":
					label = "供应商"
				case "status":
					label = "状态"
				}
				changes = append(changes, fmt.Sprintf("%s %v→%v", label, ov, nv))
			}
		}
		if len(changes) > 0 {
			return strings.Join(changes, ", ")
		}
		if n, ok := new["name"]; ok {
			return fmt.Sprintf("修改: %v", n)
		}
	}
	if a.NewData != nil {
		vals := make([]string, 0, 3)
		for _, k := range []string{"code", "name", "batch_no"} {
			if v, ok := (*a.NewData)[k]; ok {
				vals = append(vals, fmt.Sprintf("%v", v))
				if len(vals) >= 2 {
					break
				}
			}
		}
		if len(vals) > 0 {
			return strings.Join(vals, " ")
		}
	}
	return tableLabel(table) + "操作"
}

func partCodeName(m *map[string]any) string {
	if m == nil {
		return ""
	}
	code, _ := (*m)["code"].(string)
	name, _ := (*m)["name"].(string)
	if code != "" && name != "" {
		return fmt.Sprintf("[%s]%s ", code, name)
	}
	if code != "" {
		return fmt.Sprintf("[%s] ", code)
	}
	if name != "" {
		return name + " "
	}
	return ""
}

func partSupplier(m *map[string]any) string {
	if m == nil {
		return ""
	}
	s, _ := (*m)["supplier"].(string)
	if s != "" {
		return fmt.Sprintf("(%s) ", s)
	}
	return ""
}

func getFloat(m map[string]any, key string) float64 {
	v, ok := m[key]
	if !ok {
		return 0
	}
	f, _ := toFloat64(v)
	return f
}

func toFloat64(v any) (float64, bool) {
	switch val := v.(type) {
	case float64:
		return val, true
	case int:
		return float64(val), true
	case int64:
		return float64(val), true
	case string:
		f := 0.0
		_, _ = fmt.Sscanf(val, "%f", &f)
		return f, true
	default:
		return 0, false
	}
}

// ---- 结构化显示 ----

type changeItem struct {
	Field string
	Old   string
	New   string
}

var auditFieldLabels = map[string]string{
	"code": "编码", "name": "名称", "spec": "规格", "unit": "单位",
	"part_type": "分类", "stock_qty": "库存", "warn_qty": "预警", "supplier": "供应商",
	"status": "状态", "quantity": "用量", "loss_rate": "损耗率", "remark": "备注",
	"replaceable": "可替换", "use_mode": "用量模式", "batch_no": "批次号",
	"plan_qty": "计划数量", "produced_qty": "完成数量", "customer": "客户",
	"part_batch_no": "零件批次号", "used_qty": "使用数量", "in_qty": "入库数量",
	"deduct": "消耗数量", "old_stock": "原库存", "new_stock": "新库存", "diff": "差异",
	"batch_id": "批次ID", "part_id": "零件ID", "product_id": "产品ID",
}

var auditFieldOrder = map[string][]string{
	"products":         {"code", "name", "spec", "unit", "status"},
	"parts":            {"code", "name", "spec", "unit", "part_type", "stock_qty", "warn_qty", "status", "supplier"},
	"bom_items":        {"product_id", "part_id", "quantity", "loss_rate", "replaceable", "use_mode", "remark"},
	"product_batches":  {"batch_no", "product_id", "plan_qty", "produced_qty", "status", "customer"},
	"batch_trace":      {"part_batch_no", "used_qty", "supplier"},
	"batch_skip_parts": {"batch_id", "part_id"},
}

func auditFieldLabel(k string) string {
	if l, ok := auditFieldLabels[k]; ok {
		return l
	}
	return k
}

func auditFieldOrderFor(table string) []string {
	if o, ok := auditFieldOrder[table]; ok {
		return o
	}
	return []string{"code", "name", "batch_no", "part_batch_no"}
}

func auditValue(table, field string, v any) string {
	if v == nil {
		return ""
	}
	switch field {
	case "status":
		n, _ := toFloat64(v)
		if table == "product_batches" {
			_, _, txt := batchStatusStyle(int(n))
			return txt
		}
		if int(n) == 1 {
			return "启用"
		}
		return "停用"
	case "replaceable":
		n, _ := toFloat64(v)
		if int(n) == 1 {
			return "是"
		}
		return "否"
	case "use_mode":
		n, _ := toFloat64(v)
		if int(n) == 1 {
			return "每M台用1个"
		}
		return "每台用N个"
	}
	switch x := v.(type) {
	case float64:
		if x == float64(int64(x)) {
			return fmt.Sprintf("%d", int64(x))
		}
		return fmt.Sprintf("%.2f", x)
	case string:
		return x
	case bool:
		if x {
			return "是"
		}
		return "否"
	default:
		return fmt.Sprintf("%v", v)
	}
}

func auditObject(a model.AuditLog) string {
	for _, m := range []*map[string]any{a.NewData, a.OldData} {
		if s := objectFromMap(m); s != "" {
			return s
		}
	}
	return tableLabel(a.TableName)
}

func objectFromMap(m *map[string]any) string {
	if m == nil {
		return ""
	}
	d := *m
	code, _ := d["code"].(string)
	name, _ := d["name"].(string)
	if code != "" || name != "" {
		switch {
		case code != "" && name != "":
			return fmt.Sprintf("[%s] %s", code, name)
		case name != "":
			return name
		default:
			return code
		}
	}
	if bn, _ := d["batch_no"].(string); bn != "" {
		return bn
	}
	if pbn, _ := d["part_batch_no"].(string); pbn != "" {
		return pbn
	}
	if pid, ok := d["part_id"]; ok && pid != nil {
		if bid, ok2 := d["batch_id"]; ok2 && bid != nil {
			return fmt.Sprintf("批次#%v / 零件#%v", bid, pid)
		}
		return fmt.Sprintf("零件#%v", pid)
	}
	if pid, ok := d["product_id"]; ok && pid != nil {
		return fmt.Sprintf("产品#%v", pid)
	}
	return ""
}

func auditFields(table string, m *map[string]any) []changeItem {
	if m == nil {
		return nil
	}
	d := *m
	var out []changeItem
	for _, k := range auditFieldOrderFor(table) {
		v, ok := d[k]
		if !ok || v == nil {
			continue
		}
		out = append(out, changeItem{auditFieldLabel(k), "", auditValue(table, k, v)})
	}
	return out
}

func auditDiff(table string, oldM, newM *map[string]any) []changeItem {
	if oldM == nil || newM == nil {
		return nil
	}
	old := *oldM
	nw := *newM
	var out []changeItem
	for _, k := range auditFieldOrderFor(table) {
		ov, oOK := old[k]
		nv, nOK := nw[k]
		if !oOK && !nOK {
			continue
		}
		os := auditValue(table, k, ov)
		ns := auditValue(table, k, nv)
		if oOK && nOK && os == ns {
			continue
		}
		out = append(out, changeItem{auditFieldLabel(k), os, ns})
	}
	return out
}

func auditChanges(a model.AuditLog) []changeItem {
	switch a.Action {
	case "STOCK_IN", "STOCK_DEDUCT", "STOCK_ADJUST":
		var out []changeItem
		if a.NewData != nil {
			d := *a.NewData
			for _, k := range []string{"in_qty", "deduct"} {
				if v, ok := d[k]; ok && v != nil {
					out = append(out, changeItem{auditFieldLabel(k), "", auditValue(a.TableName, k, v)})
				}
			}
			os, oOK := d["old_stock"]
			ns, nOK := d["new_stock"]
			if oOK && nOK {
				out = append(out, changeItem{"库存", auditValue(a.TableName, "old_stock", os), auditValue(a.TableName, "new_stock", ns)})
			}
		}
		return out
	case "UPDATE_STATUS":
		old := ""
		if a.OldData != nil {
			old = auditValue(a.TableName, "status", (*a.OldData)["status"])
		}
		nw := ""
		if a.NewData != nil {
			nw = auditValue(a.TableName, "status", (*a.NewData)["status"])
		}
		return []changeItem{{"状态", old, nw}}
	case "INSERT":
		return auditFields(a.TableName, a.NewData)
	case "DELETE":
		return auditFields(a.TableName, a.OldData)
	case "UPDATE":
		return auditDiff(a.TableName, a.OldData, a.NewData)
	default:
		if a.OldData != nil && a.NewData != nil {
			if d := auditDiff(a.TableName, a.OldData, a.NewData); len(d) > 0 {
				return d
			}
		}
		if a.NewData != nil {
			return auditFields(a.TableName, a.NewData)
		}
		if a.OldData != nil {
			return auditFields(a.TableName, a.OldData)
		}
		return nil
	}
}

func changeText(items []changeItem) string {
	if len(items) == 0 {
		return "—"
	}
	parts := make([]string, 0, len(items))
	for _, it := range items {
		switch {
		case it.Old == "" && it.New == "":
			parts = append(parts, it.Field)
		case it.Old == "":
			parts = append(parts, fmt.Sprintf("%s：%s", it.Field, it.New))
		case it.New == "":
			parts = append(parts, fmt.Sprintf("%s：%s", it.Field, it.Old))
		default:
			parts = append(parts, fmt.Sprintf("%s：%s → %s", it.Field, it.Old, it.New))
		}
	}
	return strings.Join(parts, "；")
}

func tableTypeStyle(table string) (color.Color, color.Color) {
	switch table {
	case "products":
		return badgeBlueBg, badgeBlueFg
	case "parts":
		return badgeSuccessBg, badgeSuccessFg
	case "bom_items":
		return badgeWarnBg, badgeWarnFg
	case "batch_trace":
		return badgeBlueBg, badgeBlueFg
	default:
		return badgeGrayBg, badgeGrayFg
	}
}

func updateCellWrap(cellObj fyne.CanvasObject, text string, bgColor color.Color) {
	if c, ok := cellObj.(*cellWidget); ok {
		c.setWrapped(text, bgColor)
		return
	}
	// 兼容旧的 Stack(bg,label) 单元格
	s := cellObj.(*fyne.Container)
	bg := s.Objects[0].(*canvas.Rectangle)
	lbl := s.Objects[1].(*widget.Label)
	bg.FillColor = bgColor
	bg.Refresh()
	lbl.SetText(text)
	lbl.Alignment = fyne.TextAlignCenter
	lbl.Wrapping = fyne.TextWrapWord
	lbl.TextStyle = fyne.TextStyle{}
}

func rawJSON(m *map[string]any) string {
	if m == nil {
		return "—"
	}
	b, err := json.MarshalIndent(*m, "", "  ")
	if err != nil {
		return fmt.Sprintf("%v", *m)
	}
	return string(b)
}

func (s *AuditScreen) showDetail(a model.AuditLog) {
	info := widget.NewForm(
		widget.NewFormItem("时间", widget.NewLabel(a.CreatedAt.Format("2006-01-02 15:04:05"))),
		widget.NewFormItem("类型", widget.NewLabel(tableLabel(a.TableName))),
		widget.NewFormItem("操作", widget.NewLabel(actionLabel(a.Action))),
		widget.NewFormItem("对象", widget.NewLabel(auditObject(a))),
		widget.NewFormItem("记录ID", widget.NewLabel(fmt.Sprintf("%d", a.RecordID))),
		widget.NewFormItem("操作人", widget.NewLabel(nullStr(a.Operator))),
	)

	items := auditChanges(a)
	changeBox := container.NewVBox(widget.NewLabelWithStyle("变更明细", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}))
	if len(items) == 0 {
		changeBox.Add(widget.NewLabel("（无字段变更）"))
	} else {
		for _, it := range items {
			var txt string
			switch {
			case it.Old == "":
				txt = fmt.Sprintf("%s：%s", it.Field, it.New)
			case it.New == "":
				txt = fmt.Sprintf("%s：%s", it.Field, it.Old)
			default:
				txt = fmt.Sprintf("%s：%s → %s", it.Field, it.Old, it.New)
			}
			lbl := widget.NewLabel(txt)
			lbl.Wrapping = fyne.TextWrapWord
			changeBox.Add(lbl)
		}
	}

	oldLbl := widget.NewLabel(rawJSON(a.OldData))
	oldLbl.Wrapping = fyne.TextWrapWord
	newLbl := widget.NewLabel(rawJSON(a.NewData))
	newLbl.Wrapping = fyne.TextWrapWord
	rawBox := container.NewVBox(
		widget.NewLabelWithStyle("原始数据", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		widget.NewLabelWithStyle("old_data", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		oldLbl,
		widget.NewLabelWithStyle("new_data", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		newLbl,
	)

	content := container.NewScroll(container.NewVBox(
		info,
		widget.NewSeparator(),
		changeBox,
		widget.NewSeparator(),
		rawBox,
	))
	content.SetMinSize(fyne.NewSize(640, 460))
	d := dialog.NewCustom("操作记录详情", "关闭", content, s.window)
	d.Resize(fyne.NewSize(680, 520))
	d.Show()
}
