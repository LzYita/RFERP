package ui

import (
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"app/internal/model"
)

// searchOption 是 searchSelect 的候选项：一个稳定 ID、一段展示文本、一段用于匹配的文本。
// 定义在 UI 层，避免把"可搜索"这类界面关注点加到 model 上（保持分层）。
type searchOption struct {
	ID    int64
	Label string // 下拉列表中显示的内容
	Text  string // 参与匹配的文本（建议同时包含编码与名称）
}

// containsFold 大小写无关的子串匹配；空关键字视为命中（用于"不过滤"）。
func containsFold(hay, needle string) bool {
	needle = strings.ToLower(strings.TrimSpace(needle))
	if needle == "" {
		return true
	}
	return strings.Contains(strings.ToLower(hay), needle)
}

// partOptions 把零件映射为候选项：编码 + 名称（+ 规格），三者都可被搜索到。
func partOptions(parts []model.Part) []searchOption {
	out := make([]searchOption, 0, len(parts))
	for _, p := range parts {
		label := p.Code + "  " + p.Name
		text := p.Code + " " + p.Name
		if p.Spec != nil && *p.Spec != "" {
			label += "  " + *p.Spec
			text += " " + *p.Spec
		}
		out = append(out, searchOption{ID: p.ID, Label: label, Text: text})
	}
	return out
}

// productOptions 把产品映射为候选项：编码 + 名称（+ 规格），三者都可被搜索到。
func productOptions(products []model.Product) []searchOption {
	out := make([]searchOption, 0, len(products))
	for _, p := range products {
		label := p.Code + "  " + p.Name
		text := p.Code + " " + p.Name
		if p.Spec != nil && *p.Spec != "" {
			label += "  " + *p.Spec
			text += " " + *p.Spec
		}
		out = append(out, searchOption{ID: p.ID, Label: label, Text: text})
	}
	return out
}

// newSearchEntry 创建"输入即筛选"的搜索框：文本变化时回调 onQuery。
// 供管理页对列表做即时筛选，与 searchSelect 共用同一套匹配规则（containsFold）。
func newSearchEntry(placeholder string, onQuery func(string)) *widget.Entry {
	e := widget.NewEntry()
	e.SetPlaceHolder(placeholder)
	e.OnChanged = onQuery
	return e
}

// searchSelect 是"输入即筛选"的选择器（覆盖式下拉，与 widget.Select 的弹层行为一致）：
// 键入部分文字后，在输入框**下方弹出浮层**列出匹配项（不挤压页面其它内容），
// 点击其中一项即选中；选完或清空输入后浮层自动消失。
type searchSelect struct {
	entry    *widget.Entry
	list     *widget.List
	popUp    *widget.PopUp
	all      []searchOption
	matches  []searchOption
	selected *searchOption
	setting  bool // 程序化设置输入框文本时抑制重新筛选

	// OnSelect 在用户从下拉中点选一项后触发，用于联动（如按所选产品刷新 BOM）。
	OnSelect func()
}

const searchSelectHeight = 160

func newSearchSelect(all []searchOption, placeholder string) *searchSelect {
	s := &searchSelect{all: all, matches: all}

	s.entry = widget.NewEntry()
	s.entry.SetPlaceHolder(placeholder)

	s.list = widget.NewList(
		func() int { return len(s.matches) },
		func() fyne.CanvasObject {
			l := widget.NewLabel("")
			l.Truncation = fyne.TextTruncateEllipsis
			return l
		},
		func(i widget.ListItemID, o fyne.CanvasObject) {
			if i < 0 || i >= len(s.matches) {
				return
			}
			l := o.(*widget.Label)
			l.Truncation = fyne.TextTruncateEllipsis
			l.SetText(s.matches[i].Label)
		},
	)
	s.list.OnSelected = func(i widget.ListItemID) {
		if i < 0 || i >= len(s.matches) {
			return
		}
		sel := s.matches[i]
		s.selected = &sel
		s.setting = true
		s.entry.SetText(sel.Label)
		s.setting = false
		s.hideDropdown()
		s.list.UnselectAll()
		if s.OnSelect != nil {
			s.OnSelect()
		}
	}
	s.entry.OnChanged = func(_ string) {
		if s.setting {
			return
		}
		s.selected = nil
		s.filter(s.entry.Text)
	}
	return s
}

// object 返回可放入表单/工具条的对象（就是一个输入框，下拉以浮层呈现）。
func (s *searchSelect) object() fyne.CanvasObject { return s.entry }

// setOptions 重置候选列表并清空当前选择（用于数据刷新后重建下拉内容）。
func (s *searchSelect) setOptions(all []searchOption) {
	s.all = all
	s.matches = all
	s.selected = nil
	s.setting = true
	s.entry.SetText("")
	s.setting = false
	s.hideDropdown()
}

// clear 清空输入与选择，但保留候选项。
func (s *searchSelect) clear() {
	s.selected = nil
	s.setting = true
	s.entry.SetText("")
	s.setting = false
	s.hideDropdown()
}

// setValue 程序化选中某项：只同步显示，不触发 OnSelect（避免与调用方形成回环）。
func (s *searchSelect) setValue(opt searchOption) {
	s.selected = &opt
	s.setting = true
	s.entry.SetText(opt.Label)
	s.setting = false
	s.hideDropdown()
}

// filter 按 Text 做大小写无关的子串匹配；关键字为空或无匹配时收起浮层。
func (s *searchSelect) filter(q string) {
	if strings.TrimSpace(q) == "" {
		s.matches = s.all
		s.hideDropdown()
		return
	}
	out := make([]searchOption, 0, len(s.all))
	for _, it := range s.all {
		if containsFold(it.Text, q) {
			out = append(out, it)
		}
	}
	s.matches = out
	if len(out) == 0 {
		s.hideDropdown()
		return
	}
	s.showDropdown()
}

// showDropdown 在输入框下方弹出浮层（覆盖页面内容，不改变布局）。
func (s *searchSelect) showDropdown() {
	c := fyne.CurrentApp().Driver().CanvasForObject(s.entry)
	if c == nil {
		return // 尚未挂到画布上（例如构建期）
	}
	if s.popUp == nil {
		bg := canvas.NewRectangle(theme.BackgroundColor())
		s.popUp = widget.NewPopUp(container.NewStack(bg, s.list), c)
	}
	s.list.Refresh()
	s.list.ScrollToTop()
	s.popUp.Resize(fyne.NewSize(s.dropdownWidth(), searchSelectHeight))
	pos := fyne.CurrentApp().Driver().AbsolutePositionForObject(s.entry)
	s.popUp.ShowAtPosition(pos.Add(fyne.NewPos(0, s.entry.Size().Height)))
}

// dropdownWidth 让浮层至少与输入框同宽，并尽量容纳最长的候选项（设上限，避免超宽）。
func (s *searchSelect) dropdownWidth() float32 {
	w := s.entry.Size().Width
	textSize := theme.TextSize()
	for _, m := range s.matches {
		if tw := fyne.MeasureText(m.Label, textSize, fyne.TextStyle{}).Width + 4*theme.Padding(); tw > w {
			w = tw
		}
	}
	const maxWidth = 760
	if w > maxWidth {
		w = maxWidth
	}
	return w
}

func (s *searchSelect) hideDropdown() {
	if s.popUp != nil {
		s.popUp.Hide()
	}
}

// value 返回已选项。若未点击，则退回"输入恰好等于某项展示文本"，
// 再退回"筛选结果唯一时自动采用"，以兼容直接键入完整编码的旧用法。
func (s *searchSelect) value() (searchOption, bool) {
	if s.selected != nil {
		return *s.selected, true
	}
	q := strings.TrimSpace(s.entry.Text)
	if q == "" {
		return searchOption{}, false
	}
	for _, it := range s.all {
		if strings.EqualFold(it.Label, q) {
			return it, true
		}
	}
	if len(s.matches) == 1 {
		return s.matches[0], true
	}
	return searchOption{}, false
}
