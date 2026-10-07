package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// settingsPanel 是「设置」面板：占用主界面的内容区（右侧），自带导航栏，
// 当前包含使用指南、常见问题与更新说明三页。
//
// 刻意不做成独立窗口：设置属于主界面的一个视图，做成弹窗会打断对当前业务的
// 参照（例如照着指南核对某个页面），也让用户多出一个要关掉的东西。
//
// 同时不塞进左侧业务导航：那里按业务模块组织，混进「设置」会让业务入口不纯粹。
// 入口放在左下角、退出登录上方。
type settingsPanel struct {
	version string
	onBack  func()

	navBtns []*widget.Button
	pages   []fyne.CanvasObject
	content *fyne.Container
	current int
	build   builder
}

// newSettingsPanel 组装设置面板。onBack 用于「返回」按钮，由调用方决定回到哪。
func newSettingsPanel(version, updateURL string, onBack func()) *settingsPanel {
	s := &settingsPanel{version: version, onBack: onBack}

	back := widget.NewButtonWithIcon("返回主界面", theme.NavigateBackIcon(), s.back)
	back.Importance = widget.LowImportance

	defs := []struct {
		label string
		icon  fyne.Resource
		page  fyne.CanvasObject
	}{
		{"使用指南", theme.DocumentIcon(), newGuidePage(version, updateURL)},
		{"常见问题", theme.HelpIcon(), newFAQPage()},
		{"更新说明", theme.MediaRecordIcon(), newChangelogPage().build()},
	}

	items := []fyne.CanvasObject{container.NewPadded(back)}
	for _, d := range defs {
		btn := widget.NewButtonWithIcon(d.label, d.icon, nil)
		s.navBtns = append(s.navBtns, btn)
		s.pages = append(s.pages, d.page)
		items = append(items, btn)
	}
	for i := range s.navBtns {
		idx := i
		s.navBtns[i].OnTapped = func() { s.Select(idx) }
	}

	s.content = container.NewStack(defs[0].page)
	nav := container.NewVBox(items...)

	s.build = func() fyne.CanvasObject {
		return container.NewBorder(nil, nil, container.NewPadded(nav), nil, s.content)
	}
	s.Select(0)
	return s
}

// builder 惰性建树：构造期只登记回调，避免在还没决定是否显示时就组装控件。
type builder func() fyne.CanvasObject

// Select 切换到第 idx 页，并同步导航按钮的强调状态。
func (s *settingsPanel) Select(idx int) {
	if idx < 0 || idx >= len(s.pages) {
		return
	}
	s.current = idx
	s.content.Objects = []fyne.CanvasObject{s.pages[idx]}
	s.content.Refresh()
	for i, b := range s.navBtns {
		if i == idx {
			b.Importance = widget.HighImportance
		} else {
			b.Importance = widget.MediumImportance
		}
		b.Refresh()
	}
}

// Build 返回可放进主内容区的对象。
func (s *settingsPanel) Build() fyne.CanvasObject { return s.build() }

// back 触发「返回主界面」。抽成方法以便测试直接验证回退链路。
func (s *settingsPanel) back() {
	if s.onBack != nil {
		s.onBack()
	}
}
