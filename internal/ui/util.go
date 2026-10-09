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
func autofitColumns(t *listTable, headers []string, colTexts [][]string, minW, maxW float32) {
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

// selectableTable 是 toggleTableRowSelection 需要的最小能力集合。
// 用接口而不是 *listTable，是为了让裸 *widget.Table 也能传入（既有测试即如此）。
type selectableTable interface {
	Refresh()
	UnselectAll()
}

// toggleTableRowSelection 切换表格的行选中态（第 0 行是表头，忽略）。
//
// 由 cellWidget.Tapped 调用（见 bindCellTappable 的说明），不经过 Table.OnSelected。
//
// 这里的 UnselectAll 只清 Fyne 自身的选中态，不改变滚动位置，因此可以安全调用；
// 它是兜底而非必需——正常路径下 Fyne 的 selectedCell 始终为 nil。
// 保留它是为了万一日后有别处又走了 Table.Select，也能保证同一行可以反复点击
// （Fyne 对"同一个单元格再次 Select"会直接返回、不触发 OnSelected）。
//
// 切勿改成 Select(widget.TableCellID{Row: -1, Col: -1})：Fyne 的 Table.Select
// 只校验上界（id.Row >= rows），负值会通过并调用 ScrollTo(-1, -1)，
// 而 ScrollTo 会把该"单元格"（即 y=0）滚到可视区顶部。
func toggleTableRowSelection(t selectableTable, selected *int, row int) {
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

// listTable 包裹 widget.Table，把 Fyne 自带的"点击/聚焦即滚动"行为挡在门外。
//
// 背景（用户报告的跳顶 bug 的根因）：Fyne 的 Table.Tapped 在桌面上会调用
// canvas.Focus(t)，而它把
//
//	t.currentFocus = TableCellID{row, col}
//
// 放在 canvas.Focus(t) **之后**（table.go 中 Tapped 末段）。于是 FocusGained 里的
// ScrollTo(t.currentFocus) 用到的是上一轮遗留的 currentFocus——首次为 {Row:0,Col:0}——
// 把表格滚回第一行；退出后才把 currentFocus 写成点击行，所以只有第一次跳。
// 现象与"首次进入页面点靠下的行跳回顶部、第二次不跳、选中态正常"完全吻合。
//
// 为什么不能用"预聚焦"绕开：焦点会被搜索框、按钮、弹窗抢走，一旦丢失，用户下一次
// 点击表格又会重新触发 FocusGained → 又跳顶。所以必须让 FocusGained 本身无害。
//
// 为什么单元格拦截不够：真实驱动沿对象树找 Tappable/焦点目标时，点击落在行分隔线、
// 列分隔线或空白处不会命中任何 cell，事件仍会冒泡到 Table.Tapped。
// 包装类型是最后一道闸门——无论事件从哪里来，都不会再触发 Select/ScrollTo。
//
// 代价：方向键在列表里的行间导航失效（Table 依赖私有的 currentFocus，
// 而它只在 Table.Tapped 里更新，现在 Tapped 已被屏蔽）。
// 这是暂时接受的取舍，不是完整键盘交互修复：Tab 仍可进入表格，
// 但没有焦点高亮，也不响应列表导航键；恢复导航需独立维护焦点行。
type listTable struct {
	*widget.Table
}

// newListTable 与 widget.NewTable 同参，返回受控的 listTable。
func newListTable(
	length func() (rows int, cols int),
	create func() fyne.CanvasObject,
	update func(widget.TableCellID, fyne.CanvasObject),
) *listTable {
	return &listTable{Table: widget.NewTable(length, create, update)}
}

// Tapped 屏蔽 Table 的点击处理：不 Select、不请求焦点。
// 单元格（cellWidget）会先接管绝大多数点击，这里兜住落在分隔线/空白处的那部分。
func (t *listTable) Tapped(*fyne.PointEvent) {}

// FocusGained 屏蔽 Table 的"获得焦点即滚动到 currentFocus"。
// 表格因此可以安全地被 Tab 或鼠标聚焦。
// 刻意不做任何 Refresh：Table 原实现会 RefreshItem(currentFocus)，
// 而这里 currentFocus 恒为 {0,0}，重绘只会带来无谓的像素变化。
func (t *listTable) FocusGained() {}

// FocusLost 同样屏蔽：原实现会 Refresh 去掉焦点样式，这里无事可做。
func (t *listTable) FocusLost() {}

// TypedRune / TypedKey 不转发：Table 的键盘导航依赖私有的 currentFocus，
// 转发会在首次按下方向键时把视图跳到第 1 行，比不支持更糟。
func (t *listTable) TypedRune(rune)          {}
func (t *listTable) TypedKey(*fyne.KeyEvent) {}

// cellWidget 是表格单元格：背景 + 文本。
// 文本超宽时以省略号截断（列宽已按内容自适应，只有极长内容才会被截断）。
//
// cellWidget 自己实现 fyne.Tappable 接管点击，目的是让 Table.Tapped 收不到事件。
// 见 bindCellTappable 的说明。
type cellWidget struct {
	widget.BaseWidget
	bg    *canvas.Rectangle
	label *widget.Label

	row      int           // 本单元格对应的表格行号（0 是表头）
	onTapped func(row int) // 点击回调，nil 表示不响应点击
}

func newCellWidget() *cellWidget {
	c := &cellWidget{
		bg:    canvas.NewRectangle(transparent),
		label: widget.NewLabel(""),
		row:   -1,
	}
	c.label.Truncation = fyne.TextTruncateEllipsis
	c.ExtendBaseWidget(c)
	return c
}

// Tapped 实现 fyne.Tappable。Fyne 的点击分发会自最上层的命中对象向上回溯，
// 找到第一个 Tappable 就停下并调用它。因为每个单元格都是 Tappable，
// 点击不会再冒泡到 Table.Tapped，也就不会触发 Table.Select。
func (c *cellWidget) Tapped(*fyne.PointEvent) {
	if c.onTapped == nil {
		return
	}
	c.onTapped(c.row)
}

// bindCellTappable 把单元格与行号、点击回调绑定。必须在表格的 UpdateCell 回调里
// 调用（那里才有 TableCellID 的行号），且 onTapped 应是创建表格时就固定下来的同一个
// 闭包，避免每次刷新都为每个单元格新建闭包。
//
// 为什么不直接用 Table.OnSelected：
// 它必须经过 Table.Select，Select 会 ScrollTo(id)；随后 Table.Tapped 请求焦点，
// 才更新 currentFocus。首次跳顶的已核实主因是 FocusGained 使用旧焦点行，
// 详见 listTable 注释，不能将其归因于点击必然命中 ScrollTo 的某个坐标分支。
// 单元格直接驱动选择同时避开 Select 的滚动和原生点击的焦点路径；
// 行高亮仍由各页面自己的 selected 状态绘制。
func bindCellTappable(o fyne.CanvasObject, row int, onTapped func(row int)) {
	if c, ok := o.(*cellWidget); ok {
		c.row = row
		c.onTapped = onTapped
	}
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
	case "RESTORE":
		// 整库恢复：用蓝色系，区别于日常增删改
		return badgeBlueBg, badgeBlueFg
	case "CLEAR":
		// 清空业务数据：与删除同级，用红色警示
		return badgeErrorBg, badgeErrorFg
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

// fixedWidthBox 给单个子对象一个固定宽度，高度随内容。
//
// 用途：widget.Label 设置 Truncation 后，MinSize 会缩成一个省略号的宽度
// （约 21px），放进 HBox/Grid 后按 MinSize 分配宽度，文字直接变成 "…"。
// 需要"固定宽度 + 超出截断"时，用它把标签包起来。
type fixedWidthBox struct {
	widget.BaseWidget
	obj   fyne.CanvasObject
	width float32
}

func newFixedWidthBox(obj fyne.CanvasObject, width float32) *fixedWidthBox {
	b := &fixedWidthBox{obj: obj, width: width}
	b.ExtendBaseWidget(b)
	return b
}

func (b *fixedWidthBox) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(b.obj)
}

func (b *fixedWidthBox) MinSize() fyne.Size {
	m := b.obj.MinSize()
	return fyne.NewSize(b.width, m.Height)
}

func (b *fixedWidthBox) Resize(size fyne.Size) {
	b.BaseWidget.Resize(size)
	b.obj.Resize(fyne.NewSize(size.Width, b.obj.MinSize().Height))
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
