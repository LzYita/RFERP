package ui

import (
	"testing"

	"app/internal/model"
	"app/internal/usecase"
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
)

// Only the catalog query is used by Build/Refresh; no database is opened.
type selectionCatalog struct {
	usecase.Applications
	products []model.Product
}

func (s *selectionCatalog) ListProducts() ([]model.Product, error) { return s.products, nil }

func TestProductSelectionAfterFilterAndRefresh(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	svc := &selectionCatalog{products: []model.Product{
		{Code: "A", Name: "Alpha", Unit: "个"},
		{Code: "B", Name: "Beta", Unit: "个"},
	}}
	w := test.NewWindow(nil)
	defer w.Close()
	screen := NewProductScreen(svc, w)
	w.SetContent(screen.Build())
	w.Resize(fyne.NewSize(1200, 500))
	w.Show()
	w.Canvas().Capture()

	// Inspect the actual screen's rendered cells, including reused UpdateCell bindings.
	find := func(text string) *cellWidget {
		var found *cellWidget
		var walk func(fyne.CanvasObject)
		walk = func(o fyne.CanvasObject) {
			if c, ok := o.(*cellWidget); ok && c.label.Text == text {
				found = c
			}
			if c, ok := o.(*fyne.Container); ok {
				for _, child := range c.Objects {
					walk(child)
				}
			}
			if wd, ok := o.(fyne.Widget); ok {
				for _, child := range test.WidgetRenderer(wd).Objects() {
					walk(child)
				}
			}
		}
		walk(screen.table)
		if found == nil {
			t.Fatalf("rendered cell %q not found", text)
		}
		return found
	}
	assertSelected := func(code string) {
		t.Helper()
		cell := find(code)
		cell.Tapped(&fyne.PointEvent{})
		if screen.selected != cell.row || screen.data[screen.selected-1].Code != code {
			t.Fatalf("selected=%d for %s", screen.selected, code)
		}
		w.Canvas().Capture()
		check := find("☑")
		if check.row != screen.selected {
			t.Fatal("checkbox highlight is on the wrong row")
		}
		cell.Tapped(&fyne.PointEvent{})
		if screen.selected != -1 {
			t.Fatal("second tap did not deselect")
		}
		w.Canvas().Capture()
	}
	assertSelected("B")
	screen.query = "Beta"
	screen.applyFilter()
	w.Canvas().Capture()
	if len(screen.data) != 1 || screen.selected != -1 {
		t.Fatal("filter did not reset selection")
	}
	assertSelected("B") // B moved from row 2 to row 1.
	svc.products = []model.Product{{Code: "C", Name: "Beta replacement", Unit: "个"}}
	screen.Refresh()
	w.Canvas().Capture()
	if len(screen.data) != 1 || screen.selected != -1 {
		t.Fatal("refresh did not reset selection")
	}
	assertSelected("C")
}
