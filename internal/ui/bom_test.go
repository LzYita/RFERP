package ui

import (
	"testing"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"app/internal/model"
)

// BOM 页"查询下拉"与"名称/编码/规格联动"必须双向一致。
func TestBOMCascadeAndPickerStayInSync(t *testing.T) {
	test.NewApp()

	spec := "5V"
	products := []model.Product{
		{ID: 1, Code: "P001", Name: "电水壶", Spec: &spec},
		{ID: 2, Code: "P002", Name: "榨汁机"},
	}
	s := &BOMScreen{products: products}
	s.nameSel = widget.NewSelect([]string{"电水壶", "榨汁机"}, nil)
	s.codeSel = widget.NewSelect([]string{}, nil)
	s.specSel = widget.NewSelect([]string{}, nil)
	s.picker = newSearchSelect(productOptions(products), "查询")

	// ① 查询下拉选中 P002 → 联动选择应同步为 榨汁机 / P002
	s.picker.setValue(productOptions(products)[1])
	s.syncCascadeFromPicker()
	if s.nameSel.Selected != "榨汁机" || s.codeSel.Selected != "P002" {
		t.Fatalf("查询→联动 未同步: name=%q code=%q", s.nameSel.Selected, s.codeSel.Selected)
	}
	if pid, ok := s.resolveCascadeID(); !ok || pid != 2 {
		t.Fatalf("resolveCascadeID = %d,%v; want 2,true", pid, ok)
	}
	if pid, ok := s.resolveProductID(); !ok || pid != 2 {
		t.Fatalf("resolveProductID = %d,%v; want 2,true", pid, ok)
	}

	// ② 联动选择选中 P001（含规格 5V）→ 查询框应同步显示同一产品
	s.nameSel.Selected = "电水壶"
	s.codeSel.Options = []string{"P001"}
	s.codeSel.Selected = "P001"
	s.specSel.Options = []string{"5V"}
	s.specSel.Selected = "5V"
	s.specSel.Enable()
	s.syncPickerFromCascade()
	if got, ok := s.picker.value(); !ok || got.ID != 1 {
		t.Fatalf("联动→查询 未同步: %+v %v", got, ok)
	}
	if s.picker.entry.Text == "" {
		t.Fatal("联动→查询 未回填显示文本")
	}

	// ③ 清空后两者都不再解析出产品
	s.clearPicker()
	s.clearCascade()
	if _, ok := s.resolveProductID(); ok {
		t.Fatal("清空后不应再解析出产品")
	}
}
