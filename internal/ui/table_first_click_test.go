package ui

import (
	"fmt"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// buildListTable 复刻 product/part/bom/batch/users 的列表布局：
// container.NewBorder(topBar, statusLabel, nil, nil, table)。
// 刻意与生产代码保持一致——用 newListTable 包装、单元格接管点击。
func buildListTable(rows int) (*listTable, fyne.Window, *int) {
	selected := -1
	var tb *listTable
	tb = newListTable(
		func() (int, int) { return rows, 1 },
		makeCellTmpl,
		func(tci widget.TableCellID, o fyne.CanvasObject) {
			bindCellTappable(o, tci.Row, func(row int) {
				toggleTableRowSelection(tb, &selected, row)
			})
			if tci.Row == 0 {
				updateCell(o, "表头", true, headerColor)
				return
			}
			updateCell(o, fmt.Sprintf("row-%03d", tci.Row), false, transparent)
		},
	)
	tb.SetColumnWidth(0, 220)
	w := test.NewWindow(container.NewBorder(
		widget.NewLabel("工具栏"), widget.NewLabel("状态"), nil, nil, tb))
	w.Resize(fyne.NewSize(300, 400))
	// 必须 Show：否则窗口没有真正 layout，test.TapCanvas 的命中测试找不到单元格，
	// 点击会静默丢失，测试就成了假阳性（本文件第一版就栽在这里）。
	w.Show()
	return tb, w, &selected
}

// TestTableFocusGainedScrollsToTop 记录本 bug 的根因：Fyne 的 Table 一旦获得焦点，
// 就会把表格滚回顶部。
//
// Fyne 的 Table.Tapped 在桌面上会调用 canvas.Focus(t)，而它把
//
//	t.currentFocus = TableCellID{row, col}
//
// 放在 canvas.Focus(t) **之后**（table.go 中 Tapped 的末段）。于是 FocusGained 里的
// ScrollTo(t.currentFocus) 用到的是上一轮遗留的 currentFocus——首次为 {Row:0,Col:0}——
// 于是把表格滚到第一行；之后 currentFocus 已被写成上次点击的行，所以只有第一次跳。
// 这正好对应"首次进入页面、点选靠下的行会跳回顶部，第二次起不跳，且选中态正常"。
//
// 我们的规避方式见 listTable：屏蔽 FocusGained，让表格聚焦不再引发滚动。
//
// 本测试刻意使用**裸 widget.Table**（而非生产的 listTable），以便在 Fyne 改掉这个
// 行为时能立刻发现。测试驱动的 handleFocusOnTap 不向上回溯，headless 下点击永远拿不到
// 焦点，所以只能直接调用这个公开方法来验证。
//
// 若此测试开始 skip，说明 Fyne 改了 FocusGained 的滚动行为，需要重新评估
// listTable 是否还有必要、以及能否简化。
func TestTableFocusGainedScrollsToTop(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()

	// 裸 Table：复现 Fyne 原生行为
	bare := widget.NewTable(
		func() (int, int) { return 200, 1 },
		makeCellTmpl,
		func(tci widget.TableCellID, o fyne.CanvasObject) {
			updateCell(o, fmt.Sprintf("row-%03d", tci.Row), false, transparent)
		},
	)
	bare.SetColumnWidth(0, 220)
	w := test.NewWindow(container.NewBorder(
		widget.NewLabel("工具栏"), widget.NewLabel("状态"), nil, nil, bare))
	w.Resize(fyne.NewSize(300, 400))
	w.Show()
	defer w.Close()

	w.Canvas().Capture()
	bare.ScrollTo(widget.TableCellID{Row: 150, Col: 0})
	w.Canvas().Capture()
	before := w.Canvas().Capture()

	bare.FocusGained()

	if sameImage(before, w.Canvas().Capture()) {
		t.Skip("Fyne 已改变 FocusGained 的滚动行为：请重新评估 listTable 是否还有必要")
	}
}

// TestTapCellDoesNotScrollTable 回归守卫：点选单元格不得改变表格滚动位置。
//
// 背景：用户报告"首次进入列表页、滚轮向下滚动后点选靠下的行，表格跳回顶部"，
// 第二次起不再跳，选中态始终正常。根因见 TestTableFocusGainedScrollsToTop。
//
// 修复：让 cellWidget 自己实现 fyne.Tappable 接管点击（见 bindCellTappable），
// 点击不再冒泡到 Table.Tapped，Select/ScrollTo/canvas.Focus 这条路径根本不会执行。
//
// 局限（重要）：本测试**无法**复现用户报告的原始场景。Fyne 测试驱动对
// widget.Table 的滚轮事件是空操作——实测 test.Scroll 滚 1/5/20 次画面均不变，
// test.Drag 拖滚动条同样无效；只有 ScrollTo/ScrollToBottom 能真正滚动。
// 焦点路径更是完全测不到：测试驱动的 handleFocusOnTap 只看被点对象本身，
// 不像真实驱动那样沿对象树向上回溯找 fyne.Focusable。
// 因此这里改用 ScrollTo 制造"视口停在靠下位置"的状态，并断言点选不改变它。
// 该断言直接盯住修复依赖的不变量（点单元格不触发滚动），是真回归守卫；
// 但真实鼠标操作下的最终观感仍需在 GUI 中确认。
func TestTapCellDoesNotScrollTable(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()

	cases := []struct {
		name   string
		rows   int
		anchor int // 用 ScrollTo 把视口停在这个行附近
		clickY float32
	}{
		{"200行/停靠150/点上部", 200, 150, 60},
		{"200行/停靠150/点中部", 200, 150, 200},
		{"200行/停靠80/点上部", 200, 80, 60},
		{"500行/停靠300/点上部", 500, 300, 60},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			table, w, selected := buildListTable(tc.rows)
			defer w.Close()

			w.Canvas().Capture()
			table.ScrollTo(widget.TableCellID{Row: tc.anchor, Col: 0})
			w.Canvas().Capture()

			before := w.Canvas().Capture()
			test.TapCanvas(w.Canvas(), fyne.Position{X: 60, Y: tc.clickY})

			if *selected <= 0 {
				t.Fatalf("点击未命中任何数据行（selected=%d），测试无意义", *selected)
			}
			if after := w.Canvas().Capture(); !sameImage(before, after) {
				t.Fatalf("点选后视图发生变化：表格被滚动到别处（应保持原滚动位置）")
			}
		})
	}
}

// tapFirstDataRow 逐个尝试若干 y，返回第一个能选中数据行的坐标。
// 行的像素位置会随 layout/focus 有几像素漂移，且个别 y 正落在行分隔线上不触发
// 分发，所以这里不硬编码坐标——几何由 TestCellsAreTappableAndReachable 专门覆盖。
func tapFirstDataRow(t *testing.T, w fyne.Window, selected *int) float32 {
	t.Helper()
	for _, y := range []float32{100, 140, 180, 220, 260, 300} {
		*selected = -1
		test.TapCanvas(w.Canvas(), fyne.Position{X: 60, Y: y})
		if *selected > 0 {
			return y
		}
	}
	t.Fatal("所有候选坐标都没能选中数据行")
	return 0
}

// TestTapCellSelectsAndDeselects 确认修复没有破坏选中功能。
func TestTapCellSelectsAndDeselects(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()

	_, w, selected := buildListTable(200)
	defer w.Close()
	w.Canvas().Capture()

	// 点表格顶部（表头所在行）不应选中任何行
	test.TapCanvas(w.Canvas(), fyne.Position{X: 60, Y: 45})
	if *selected != -1 {
		t.Fatalf("点击表头后 selected = %d，期望保持 -1", *selected)
	}

	// 点数据行应选中
	y := tapFirstDataRow(t, w, selected)
	first := *selected
	if first <= 0 {
		t.Fatalf("点击数据行后 selected = %d，期望 >0", first)
	}

	// 再点同一行应取消
	test.TapCanvas(w.Canvas(), fyne.Position{X: 60, Y: y})
	if *selected != -1 {
		t.Fatalf("再次点击同一行后 selected = %d，期望 -1（取消选中）", *selected)
	}

	// 取消后还能再次选中同一行（说明选中态确实被清掉了）
	test.TapCanvas(w.Canvas(), fyne.Position{X: 60, Y: y})
	if *selected != first {
		t.Fatalf("重新点击后 selected = %d，期望 %d", *selected, first)
	}
}

// TestCellsAreTappableAndReachable 结构性守卫：
// 单元格必须在渲染树里、必须实现 fyne.Tappable、必须能被真实命中测试选中。
// 命中测试走的是 driver.FindObjectAtPositionMatching，与真实窗口驱动共用同一实现，
// 所以这里能验证"点击会先落到单元格、不会落到 Table"。
func TestCellsAreTappableAndReachable(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()

	table, w, selected := buildListTable(200)
	defer w.Close()
	w.Canvas().Capture()

	// 渲染树里应有 cellWidget，且都实现 fyne.Tappable
	r := test.WidgetRenderer(table)
	cells, tappable := 0, 0
	var walk func(objs []fyne.CanvasObject)
	walk = func(objs []fyne.CanvasObject) {
		for _, o := range objs {
			if _, ok := o.(*cellWidget); ok {
				cells++
				if _, ok := o.(fyne.Tappable); ok {
					tappable++
				}
			}
			if wd, ok := o.(fyne.Widget); ok {
				walk(test.WidgetRenderer(wd).Objects())
			}
		}
	}
	walk(r.Objects())
	if cells == 0 {
		t.Fatal("渲染树中找不到 cellWidget")
	}
	if cells != tappable {
		t.Fatalf("cellWidget 共 %d 个，其中实现 fyne.Tappable 的只有 %d 个", cells, tappable)
	}

	// 命中测试应把点击送到单元格：点不同的 y 应选中递增的行号
	var picked []int
	for _, y := range []float32{100, 150, 200, 250} {
		before := *selected
		test.TapCanvas(w.Canvas(), fyne.Position{X: 60, Y: y})
		if *selected == before {
			t.Fatalf("y=%.0f 未命中任何单元格（selected 仍为 %d）", y, before)
		}
		picked = append(picked, *selected)
	}
	for i := 1; i < len(picked); i++ {
		if picked[i] <= picked[i-1] {
			t.Fatalf("y 增大时选中行应递增，实际 %v", picked)
		}
	}
}
