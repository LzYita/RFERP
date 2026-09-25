package ui

import (
	"fmt"
	"image/color"
	"math"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"app/internal/model"
)

var (
	selColor    color.Color = clrSelected
	warnColor   color.Color = clrWarnBg
	zebraColor  color.Color = clrZebra
	headerColor color.Color = clrHeader
	transparent color.Color = color.Transparent
)

func makeCellTmpl() fyne.CanvasObject {
	return newCellWidget()
}

// updateCell 统一居中显示，badge=true 时加粗并带强调底色
func updateCell(cellObj fyne.CanvasObject, text string, bold bool, bgColor color.Color) {
	updateCellEx(cellObj, text, bold, bgColor, false)
}

func updateCellEx(cellObj fyne.CanvasObject, text string, bold bool, bgColor color.Color, badge bool) {
	if c, ok := cellObj.(*cellWidget); ok {
		c.set(text, badge || bold, bgColor)
		return
	}
	// 兼容旧的 Stack(bg,label) 单元格
	s := cellObj.(*fyne.Container)
	bg := s.Objects[0].(*canvas.Rectangle)
	lbl := s.Objects[1].(*widget.Label)
	bg.FillColor = bgColor
	bg.Refresh()
	lbl.Truncation = fyne.TextTruncateEllipsis
	lbl.SetText(text)
	lbl.Alignment = fyne.TextAlignCenter
	if badge || bold {
		lbl.TextStyle = fyne.TextStyle{Bold: true}
	} else {
		lbl.TextStyle = fyne.TextStyle{}
	}
}

// statusBadge 根据状态返回 (背景色, 前景色)
// textWidth 估算文本在当前主题下的像素宽度。
func textWidth(s string, bold bool) float32 {
	style := fyne.TextStyle{}
	if bold {
		style.Bold = true
	}
	return fyne.MeasureText(s, theme.TextSize(), style).Width
}

// autofitColumns 依据表头与各列文本调整列宽，让内容尽量完整显示。
// minW/maxW 为列宽下限/上限；超过上限的列由单元格以省略号截断。
func autofitColumns(t *widget.Table, headers []string, colTexts [][]string, minW, maxW float32) {
	if t == nil {
		return
	}
	for col := range headers {
		w := textWidth(headers[col], true)
		for _, rowText := range colTexts {
			if col >= len(rowText) {
				continue
			}
			if tw := textWidth(rowText[col], false); tw > w {
				w = tw
			}
		}
		w += 4 * theme.Padding()
		if w < minW {
			w = minW
		}
		if w > maxW {
			w = maxW
		}
		t.SetColumnWidth(col, w)
	}
}

// toggleTableRowSelection 切换表格的行选中态（第 0 行是表头，忽略）。
//
// 这里必须用 UnselectAll 清掉 Fyne 自身的选中态，**不能**用
// Select(widget.TableCellID{Row: -1, Col: -1})：
// Fyne 的 Table.Select 只校验上界（id.Row >= rows），负值会通过并调用
// ScrollTo(-1, -1)，而 ScrollTo 会把该"单元格"（即 y=0）滚到可视区顶部——
// 于是用户点选靠下的行时，表格会突然跳回顶部。
// UnselectAll 只清选中状态，不改变滚动位置。
//
// 清状态是必要的：Fyne 对"同一个单元格再次 Select"会直接返回、不触发
// OnSelected，那样同一行就无法再次点击（也就无法取消选中）。
func toggleTableRowSelection(t *widget.Table, selected *int, row int) {
	if t == nil || row <= 0 {
		return
	}
	if *selected == row {
		*selected = -1
	} else {
		*selected = row
	}
	t.Refresh()
	t.UnselectAll()
}

// cellWidget 是表格单元格：背景 + 文本。
// 文本超宽时以省略号截断（列宽已按内容自适应，只有极长内容才会被截断）。
type cellWidget struct {
	widget.BaseWidget
	bg    *canvas.Rectangle
	label *widget.Label
}

func newCellWidget() *cellWidget {
	c := &cellWidget{
		bg:    canvas.NewRectangle(transparent),
		label: widget.NewLabel(""),
	}
	c.label.Truncation = fyne.TextTruncateEllipsis
	c.ExtendBaseWidget(c)
	return c
}

func (c *cellWidget) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(container.NewStack(c.bg, c.label))
}

func (c *cellWidget) set(text string, bold bool, bgColor color.Color) {
	c.bg.FillColor = bgColor
	c.bg.Refresh()
	c.label.Truncation = fyne.TextTruncateEllipsis
	c.label.Wrapping = fyne.TextWrapOff
	c.label.SetText(text)
	c.label.Alignment = fyne.TextAlignCenter
	if bold {
		c.label.TextStyle = fyne.TextStyle{Bold: true}
	} else {
		c.label.TextStyle = fyne.TextStyle{}
	}
}

// setWrapped 与 set 相同，但文本按词换行（用于「变更明细」这类多行单元格，
// 行高由调用方按估算的行数设置）。
func (c *cellWidget) setWrapped(text string, bgColor color.Color) {
	c.bg.FillColor = bgColor
	c.bg.Refresh()
	c.label.Truncation = fyne.TextTruncateOff
	c.label.Wrapping = fyne.TextWrapWord
	c.label.SetText(text)
	c.label.Alignment = fyne.TextAlignCenter
	c.label.TextStyle = fyne.TextStyle{}
}

func productStatusStyle(status int) (color.Color, color.Color) {
	if status == 1 {
		return badgeSuccessBg, badgeSuccessFg
	}
	return badgeGrayBg, badgeGrayFg
}

func productStatusText(status int) string {
	if status == 1 {
		return "启用"
	}
	return "停用"
}

func partStatusStyle(lowStock bool, status int) (color.Color, color.Color, string) {
	if lowStock {
		return badgeErrorBg, badgeErrorFg, "库存紧张"
	}
	if status == 1 {
		return badgeSuccessBg, badgeSuccessFg, "启用"
	}
	return badgeGrayBg, badgeGrayFg, "停用"
}

func batchStatusStyle(status int) (color.Color, color.Color, string) {
	switch status {
	case 0:
		return badgeGrayBg, badgeGrayFg, "待生产"
	case 1:
		return badgeBlueBg, badgeBlueFg, "生产中"
	case 2:
		return badgeSuccessBg, badgeSuccessFg, "已完成"
	case 3:
		return badgeWarnBg, badgeWarnFg, "已暂停"
	case 4:
		return badgeErrorBg, badgeErrorFg, "已撤销"
	}
	return badgeGrayBg, badgeGrayFg, "未知"
}

func actionStyle(action string) (color.Color, color.Color) {
	switch action {
	case "DELETE", "REVOKE":
		return badgeErrorBg, badgeErrorFg
	case "INSERT", "STOCK_IN":
		return badgeSuccessBg, badgeSuccessFg
	case "UPDATE", "UPDATE_STATUS", "STOCK_ADJUST":
		return badgeBlueBg, badgeBlueFg
	case "STOCK_DEDUCT":
		return badgeWarnBg, badgeWarnFg
	}
	return badgeGrayBg, badgeGrayFg
}

// 判断数据行是否显示斑马纹背景（表头不算）
func dataRowBG(row int, sel bool) color.Color {
	if sel {
		return selColor
	}
	if row%2 == 0 {
		return zebraColor
	}
	return transparent
}

func withImportance(b *widget.Button, imp widget.Importance) *widget.Button {
	b.Importance = imp
	return b
}

func parseIntText(text string) (int, error) {
	value, err := strconv.Atoi(strings.TrimSpace(text))
	if err != nil {
		return 0, fmt.Errorf("请输入有效整数: %w", err)
	}
	return value, nil
}

func parseFloatText(text string) (float64, error) {
	value, err := strconv.ParseFloat(strings.TrimSpace(text), 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		if err == nil {
			err = fmt.Errorf("数值必须有限")
		}
		return 0, fmt.Errorf("请输入有效数值: %w", err)
	}
	return value, nil
}

// bomConsumeQty 计算指定产品数量下某BOM项的零件消耗量
// use_mode: 0=每台产品用N个零件 -> 台数*N*(1+损耗率%)
// use_mode: 1=每M台产品用1个零件(包装箱) -> ceil(台数/M)，再按损耗率上浮后向上取整
func bomConsumeQty(planQty int, b model.BOMItem) float64 {
	base := float64(planQty) * b.Quantity
	if b.UseMode == 1 {
		m := b.Quantity
		if m <= 0 {
			m = 1
		}
		base = math.Ceil(float64(planQty) / m)
	}
	consume := base * (1 + b.LossRate/100)
	if b.UseMode == 1 {
		consume = math.Ceil(consume)
	}
	return consume
}
