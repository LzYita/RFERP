package ui

import (
	"fmt"
	"image"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// sameImage 逐像素比较两张画布截图。
func sameImage(a, b image.Image) bool {
	if a.Bounds() != b.Bounds() {
		return false
	}
	bounds := a.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r1, g1, b1, a1 := a.At(x, y).RGBA()
			r2, g2, b2, a2 := b.At(x, y).RGBA()
			if r1 != r2 || g1 != g2 || b1 != b2 || a1 != a2 {
				return false
			}
		}
	}
	return true
}

func newTestTable(rows int) *widget.Table {
	return widget.NewTable(
		func() (int, int) { return rows, 1 },
		func() fyne.CanvasObject { return widget.NewLabel("") },
		func(id widget.TableCellID, o fyne.CanvasObject) {
			o.(*widget.Label).SetText(fmt.Sprintf("row-%02d", id.Row))
		},
	)
}

// 回归：点选靠下的行不能把表格滚回顶部。
//
// 背景：Fyne 的 Table.Select 只校验上界（id.Row >= rows），负值会通过并调用
// ScrollTo(-1, -1)，把该"单元格"（即 y=0）滚到可视区顶部。此前各列表页正是用
// Select(Row:-1, Col:-1) 清 Fyne 层选中态，于是用户点选靠下的行时会突然跳回顶部。
// toggleTableRowSelection 改用 UnselectAll，画面必须保持不变。
func TestToggleTableRowSelectionKeepsScrollPosition(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()

	table := newTestTable(60)
	w := test.NewWindow(container.NewStack(table))
	defer w.Close()
	w.Resize(fyne.NewSize(240, 240))

	table.ScrollToBottom()
	before := w.Canvas().Capture()

	selected := -1
	toggleTableRowSelection(table, &selected, 45)

	if selected != 45 {
		t.Fatalf("选中行 = %d，期望 45", selected)
	}
	if after := w.Canvas().Capture(); !sameImage(before, after) {
		t.Fatal("点选后画面发生变化：表格被滚动了，应保持原滚动位置")
	}
}

// 记录 Fyne 的这个坑，同时证明上面的回归测试"有感知力"：
// 用 Select(Row:-1, Col:-1) 清选中态确实会把表格滚回顶部。
//
// 若此测试开始 skip，说明 Fyne 改了 Select 对负行的处理，
// 需要重新评估 toggleTableRowSelection 里 UnselectAll 的必要性。
func TestNegativeRowSelectScrollsToTop(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()

	table := newTestTable(60)
	w := test.NewWindow(container.NewStack(table))
	defer w.Close()
	w.Resize(fyne.NewSize(240, 240))

	table.ScrollToBottom()
	before := w.Canvas().Capture()

	table.Select(widget.TableCellID{Row: -1, Col: -1})

	if sameImage(before, w.Canvas().Capture()) {
		t.Skip("Fyne 已改变 Select 对负行的处理：请重新评估 toggleTableRowSelection")
	}
}

func TestToggleTableRowSelectionState(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()

	table := newTestTable(3)
	selected := -1

	// 表头（第 0 行）不可选中
	toggleTableRowSelection(table, &selected, 0)
	if selected != -1 {
		t.Fatalf("点表头后 selected = %d，期望 -1", selected)
	}

	toggleTableRowSelection(table, &selected, 2)
	if selected != 2 {
		t.Fatalf("首次点选后 selected = %d，期望 2", selected)
	}

	// 再点同一行：取消选中
	toggleTableRowSelection(table, &selected, 2)
	if selected != -1 {
		t.Fatalf("再次点选后 selected = %d，期望 -1", selected)
	}

	// 取消后仍能再次选中同一行（说明 Fyne 层选中态确实被清掉了）
	toggleTableRowSelection(table, &selected, 2)
	if selected != 2 {
		t.Fatalf("取消后重新点选 selected = %d，期望 2", selected)
	}

	// 换一行
	toggleTableRowSelection(table, &selected, 1)
	if selected != 1 {
		t.Fatalf("换行后 selected = %d，期望 1", selected)
	}

	// 传入 nil 表格不应 panic
	toggleTableRowSelection(nil, &selected, 1)
	if selected != 1 {
		t.Fatalf("nil 表格后 selected = %d，期望保持 1", selected)
	}
}
