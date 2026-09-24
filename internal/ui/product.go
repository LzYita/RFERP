package ui

import (
	"fmt"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"app/internal/auth"
	"app/internal/model"
	"app/internal/usecase"
)

type ProductScreen struct {
	svc      usecase.Applications
	window   fyne.Window
	all      []model.Product
	data     []model.Product
	table    *widget.Table
	label    *widget.Label
	selected int
	query    string
}

func NewProductScreen(svc usecase.Applications, w fyne.Window) *ProductScreen {
	return &ProductScreen{svc: svc, window: w}
}

func (s *ProductScreen) Build() fyne.CanvasObject {
	btns := []fyne.CanvasObject{widget.NewButtonWithIcon("刷新", theme.ViewRefreshIcon(), s.Refresh)}
	if auth.CanWrite(auth.ModuleProducts) {
		btns = append(btns,
			withImportance(widget.NewButtonWithIcon("新增", theme.ContentAddIcon(), s.add), widget.HighImportance),
			widget.NewButtonWithIcon("编辑", theme.DocumentCreateIcon(), s.edit),
			withImportance(widget.NewButtonWithIcon("删除", theme.DeleteIcon(), s.delete), widget.DangerImportance),
		)
	}
	search := newSearchEntry("输入编码/名称/规格筛选", func(q string) {
		s.query = q
		s.applyFilter()
	})
	topBar := container.NewBorder(nil, nil, nil, container.NewHBox(btns...), search)

	s.label = widget.NewLabel("共 0 条记录")

	s.selected = -1
	s.table = widget.NewTable(
		func() (int, int) { return len(s.data) + 1, 6 },
		makeCellTmpl,
		func(tci widget.TableCellID, o fyne.CanvasObject) {
			sel := tci.Row == s.selected && tci.Row > 0
			if tci.Row == 0 {
				headers := []string{"编码", "名称", "规格", "单位", "状态", "选择"}
				updateCell(o, headers[tci.Col], true, headerColor)
			} else {
				idx := tci.Row - 1
				if idx >= len(s.data) {
					return
				}
				p := s.data[idx]
				bg := dataRowBG(idx, sel)
				var text string
				isBadge := false
				switch tci.Col {
				case 0:
					text = p.Code
				case 1:
					text = p.Name
				case 2:
					text = nullStr(p.Spec)
				case 3:
					text = p.Unit
				case 4:
					bbg, _ := productStatusStyle(p.Status)
					bg = bbg
					text = productStatusText(p.Status)
					isBadge = true
				case 5:
					if sel {
						text = "☑"
					} else {
						text = "☐"
					}
				}
				updateCellEx(o, text, true, bg, isBadge)
			}
		},
	)
	var selGuard bool
	s.table.OnSelected = func(id widget.TableCellID) {
		if selGuard {
			selGuard = false
			return
		}
		if id.Row == 0 {
			return
		}
		if s.selected == id.Row {
			s.selected = -1
		} else {
			s.selected = id.Row
		}
		s.table.Refresh()
		selGuard = true
		s.table.Select(widget.TableCellID{Row: -1, Col: -1})
	}
	s.table.SetColumnWidth(0, 150)
	s.table.SetColumnWidth(1, 280)
	s.table.SetColumnWidth(2, 220)
	s.table.SetColumnWidth(3, 110)
	s.table.SetColumnWidth(4, 120)
	s.table.SetColumnWidth(5, 110)

	s.Refresh()

	return container.NewBorder(topBar, s.label, nil, nil, s.table)
}

func (s *ProductScreen) Refresh() {
	list, err := s.svc.ListProducts()
	if err != nil {
		showError(s.window, "查询失败", err)
		return
	}
	s.all = list
	s.applyFilter()
}

// applyFilter 按查询关键字筛选（编码 / 名称 / 规格联动），不重新查询数据库。
func (s *ProductScreen) applyFilter() {
	filtered := make([]model.Product, 0, len(s.all))
	for _, p := range s.all {
		if containsFold(p.Code+" "+p.Name+" "+nullStr(p.Spec), s.query) {
			filtered = append(filtered, p)
		}
	}
	s.data = filtered
	if s.query == "" {
		s.label.SetText(fmt.Sprintf("共 %d 条记录", len(s.data)))
	} else {
		s.label.SetText(fmt.Sprintf("匹配 %d / 共 %d 条记录", len(s.data), len(s.all)))
	}
	if s.table != nil {
		s.selected = -1
		s.table.Refresh()
	}
}

func (s *ProductScreen) add() {
	code := widget.NewEntry()
	code.SetPlaceHolder("产品编码")
	name := widget.NewEntry()
	name.SetPlaceHolder("产品名称")
	spec := widget.NewEntry()
	spec.SetPlaceHolder("规格型号")
	unit := widget.NewSelectEntry([]string{"个", "台", "套", "只"})
	unit.SetText("个")

	items := []*widget.FormItem{
		widget.NewFormItem("编码", code),
		widget.NewFormItem("名称", name),
		widget.NewFormItem("规格", spec),
		widget.NewFormItem("单位", unit),
	}

	d := dialog.NewForm("新增产品", "确定", "取消", items, func(ok bool) {
		if !ok {
			return
		}
		op := auth.OperatorName()
		p := &model.Product{
			Code:     code.Text,
			Name:     name.Text,
			Spec:     strPtr(spec.Text),
			Unit:     unit.Text,
			Status:   1,
			Operator: &op,
		}
		_, err := s.svc.CreateProduct(p)
		if err != nil {
			showError(s.window, "新增失败", err)
			return
		}
		s.Refresh()
	}, s.window)
	d.Resize(fyne.NewSize(500, 0))
	d.Show()
}

func (s *ProductScreen) edit() {
	idx := s.selected
	if idx <= 0 || idx-1 >= len(s.data) {
		dialog.ShowInformation("提示", "请先选择一行", s.window)
		return
	}
	p := s.data[idx-1]

	code := widget.NewEntry()
	code.SetText(p.Code)
	name := widget.NewEntry()
	name.SetText(p.Name)
	spec := widget.NewEntry()
	spec.SetText(nullStr(p.Spec))
	unit := widget.NewSelectEntry([]string{"个", "台", "套", "只"})
	unit.SetText(p.Unit)
	status := widget.NewCheck("启用", nil)
	status.SetChecked(p.Status == 1)
	_ = status

	items := []*widget.FormItem{
		widget.NewFormItem("编码", code),
		widget.NewFormItem("名称", name),
		widget.NewFormItem("规格", spec),
		widget.NewFormItem("单位", unit),
	}

	d := dialog.NewForm("编辑产品", "确定", "取消", items, func(ok bool) {
		if !ok {
			return
		}
		op := auth.OperatorName()
		st := 1
		up := &model.Product{
			ID:       p.ID,
			Code:     code.Text,
			Name:     name.Text,
			Spec:     strPtr(spec.Text),
			Unit:     unit.Text,
			Status:   st,
			Version:  p.Version,
			Operator: &op,
		}
		_, err := s.svc.UpdateProduct(up)
		if err != nil {
			showError(s.window, "编辑失败", err)
			return
		}
		s.Refresh()
	}, s.window)
	d.Resize(fyne.NewSize(500, 0))
	d.Show()
}

func (s *ProductScreen) delete() {
	idx := s.selected
	if idx <= 0 || idx-1 >= len(s.data) {
		dialog.ShowInformation("提示", "请先选择一行", s.window)
		return
	}
	p := s.data[idx-1]
	dialog.NewConfirm("确认删除", fmt.Sprintf("确定删除产品 %s(%s)？", p.Name, p.Code), func(ok bool) {
		if !ok {
			return
		}
		if err := s.svc.DeleteProduct(p.ID, auth.OperatorName()); err != nil {
			showError(s.window, "删除失败", err)
			return
		}
		s.Refresh()
	}, s.window).Show()
}

func nullStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func showError(w fyne.Window, title string, err error) {
	dialog.NewError(fmt.Errorf("%s: %v", title, err), w).Show()
}
