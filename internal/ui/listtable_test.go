package ui

import (
	"fmt"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// 验证 listTable 包装没有破坏渲染：内容与尺寸应与裸 widget.Table 一致。
func TestListTableRendersLikePlainTable(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()

	build := func(wrap bool) fyne.Window {
		length := func() (int, int) { return 60, 2 }
		create := func() fyne.CanvasObject { return makeCellTmpl() }
		update := func(id widget.TableCellID, o fyne.CanvasObject) {
			if id.Row == 0 {
				updateCell(o, "表头", true, headerColor)
				return
			}
			updateCell(o, fmt.Sprintf("row-%03d", id.Row), id.Row%2 == 0, transparent)
		}
		var tb fyne.CanvasObject
		if wrap {
			tb = newListTable(length, create, update)
		} else {
			tb = widget.NewTable(length, create, update)
		}
		w := test.NewWindow(tb)
		w.Resize(fyne.NewSize(300, 240))
		w.Show()
		return w
	}

	w1 := build(false)
	defer w1.Close()
	w2 := build(true)
	defer w2.Close()

	if !sameImage(w1.Canvas().Capture(), w2.Canvas().Capture()) {
		t.Fatal("listTable 渲染结果与裸 widget.Table 不一致：包装破坏了渲染")
	}
}

// 验证 listTable 屏蔽了 FocusGained 的滚动。
func TestListTableFocusGainedDoesNotScroll(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()

	table, w, _ := buildListTable(200)
	defer w.Close()
	w.Canvas().Capture()
	table.ScrollTo(widget.TableCellID{Row: 150, Col: 0})
	w.Canvas().Capture()
	before := w.Canvas().Capture()

	table.FocusGained()

	if after := w.Canvas().Capture(); !sameImage(before, after) {
		t.Fatal("listTable.FocusGained 仍会滚动表格：跳顶未被挡住")
	}
}

// 验证 listTable 屏蔽了 Tapped：点击不应触发 Fyne 的 Select/滚动。
// 落在分隔线/空白处（没有 cell 命中）时事件会冒泡到表格本身，这条路径必须被挡住。
func TestListTableTappedDoesNotScroll(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()

	table, w, selected := buildListTable(200)
	defer w.Close()
	w.Canvas().Capture()
	table.ScrollTo(widget.TableCellID{Row: 150, Col: 0})
	w.Canvas().Capture()
	before := w.Canvas().Capture()

	// 模拟点击落在没有 cell 的位置（行分隔线附近）
	table.Tapped(&fyne.PointEvent{Position: fyne.Position{X: 60, Y: 78}})

	if *selected > 0 {
		t.Fatalf("listTable.Tapped 触发了选中（selected=%d），说明 Fyne 的 Select 仍在跑", *selected)
	}
	if after := w.Canvas().Capture(); !sameImage(before, after) {
		t.Fatal("listTable.Tapped 仍会滚动表格")
	}
}
