package ui

import "fmt"

// 本文件集中各列表页的"列宽自适应"：按当前数据的最长文本计算列宽（带上限），
// 让内容尽量完整显示。超上限的列仍由单元格截断 + 悬停提示兜底。

const (
	colMinWidth float32 = 90
	colMaxWidth float32 = 320
)

func (s *ProductScreen) fitColumns() {
	headers := []string{"编码", "名称", "规格", "单位", "状态", "选择"}
	rows := make([][]string, 0, len(s.data))
	for _, p := range s.data {
		rows = append(rows, []string{
			p.Code, p.Name, nullStr(p.Spec), p.Unit, productStatusText(p.Status), "☐",
		})
	}
	autofitColumns(s.table, headers, rows, colMinWidth, colMaxWidth)
}

func (s *BOMScreen) fitColumns() {
	headers := []string{"零件编码", "零件名称", "用量模式", "用量", "损耗率(%)", "可替换", "备注", "选择"}
	rows := make([][]string, 0, len(s.bomData))
	for _, b := range s.bomData {
		mode := "每台用N个"
		if b.UseMode == 1 {
			mode = "每M台用1个"
		}
		replaceable := "否"
		if b.Replaceable == 1 {
			replaceable = "是"
		}
		rows = append(rows, []string{
			nullStr(b.PartCode), nullStr(b.PartName), mode,
			fmt.Sprintf("%.2f", b.Quantity), fmt.Sprintf("%.1f", b.LossRate),
			replaceable, nullStr(b.Remark), "☐",
		})
	}
	autofitColumns(s.table, headers, rows, colMinWidth, colMaxWidth)
}

func (s *BatchScreen) fitColumns() {
	headers := []string{"批次号", "产品", "计划数", "完成数", "状态", "客户", "选择"}
	rows := make([][]string, 0, len(s.data))
	for _, b := range s.data {
		_, _, status := batchStatusStyle(b.Status)
		rows = append(rows, []string{
			b.BatchNo,
			fmt.Sprintf("%s [%s]", nullStr(b.ProductName), nullStr(b.ProductCode)),
			fmt.Sprintf("%d", b.PlanQty), fmt.Sprintf("%d", b.ProducedQty),
			status, nullStr(b.Customer), "☐",
		})
	}
	autofitColumns(s.table, headers, rows, colMinWidth, colMaxWidth)
}

func (s *UsersScreen) fitColumns() {
	headers := []string{"用户名", "显示名", "角色", "状态", "最后登录", "选择"}
	rows := make([][]string, 0, len(s.data))
	for _, u := range s.data {
		status := "停用"
		if u.Status == 1 {
			status = "启用"
		}
		last := ""
		if u.LastLoginAt != nil {
			last = u.LastLoginAt.Format("01-02 15:04")
		}
		rows = append(rows, []string{
			u.Username, nullStr(u.DisplayName), u.Role, status, last, "☐",
		})
	}
	autofitColumns(s.table, headers, rows, colMinWidth, colMaxWidth)
}
