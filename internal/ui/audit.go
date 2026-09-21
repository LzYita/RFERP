package ui

import (
	"fmt"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"app/internal/model"
	"app/internal/service"
)

type AuditScreen struct {
	svc     *service.Service
	window  fyne.Window
	allData []model.AuditLog
	data    []model.AuditLog
	table   *widget.Table
	label   *widget.Label
	filter  string
}

func NewAuditScreen(svc *service.Service, w fyne.Window) *AuditScreen {
	return &AuditScreen{svc: svc, window: w, filter: "all"}
}

func (s *AuditScreen) Build() fyne.CanvasObject {
	filterBar := container.NewHBox(
		widget.NewButton("全部", func() { s.setFilter("all") }),
		widget.NewButton("产品", func() { s.setFilter("products") }),
		widget.NewButton("BOM", func() { s.setFilter("bom_items") }),
		widget.NewButton("生产批次", func() { s.setFilter("product_batches") }),
		widget.NewButton("零件流水", func() { s.setFilter("parts") }),
		widget.NewButtonWithIcon("刷新", theme.ViewRefreshIcon(), s.Refresh),
	)

	s.label = widget.NewLabel("共 0 条记录")

	s.table = widget.NewTable(
		func() (int, int) { return len(s.data) + 1, 6 },
		makeCellTmpl,
		func(tci widget.TableCellID, o fyne.CanvasObject) {
			if tci.Row == 0 {
				headers := []string{"时间", "表名", "记录ID", "操作", "操作人", "内容"}
				updateCell(o, headers[tci.Col], true, headerColor)
				return
			}
			idx := tci.Row - 1
			if idx >= len(s.data) {
				return
			}
			a := s.data[idx]
			var text string
			isBadge := false
			bg := dataRowBG(idx, false)
			switch tci.Col {
			case 0:
				text = a.CreatedAt.Format("01-02 15:04")
			case 1:
				text = tableLabel(a.TableName)
			case 2:
				text = fmt.Sprintf("%d", a.RecordID)
			case 3:
				bbg, _ := actionStyle(a.Action)
				bg = bbg
				text = actionLabel(a.Action)
				isBadge = true
			case 4:
				text = nullStr(a.Operator)
			case 5:
				text = shortSummary(a)
			}
			updateCellEx(o, text, true, bg, isBadge)
		},
	)
	s.table.SetColumnWidth(0, 110)
	s.table.SetColumnWidth(1, 100)
	s.table.SetColumnWidth(2, 80)
	s.table.SetColumnWidth(3, 100)
	s.table.SetColumnWidth(4, 80)
	s.table.SetColumnWidth(5, 510)

	s.Refresh()
	return container.NewBorder(filterBar, s.label, nil, nil, s.table)
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
	case "parts":
		s.data = filterByTable(s.allData, []string{"parts"})
	default:
		s.data = s.allData
	}
	s.label.SetText(fmt.Sprintf("共 %d 条记录", len(s.data)))
	if s.table != nil {
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
	return table + "操作"
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
