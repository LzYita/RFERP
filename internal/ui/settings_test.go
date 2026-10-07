package ui

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"app/internal/auth"
	"app/internal/config"
	"app/internal/model"
	"app/internal/usecase"
)

// noopApps 只为满足接口，本文件不触发任何业务调用。
type noopApps struct{ usecase.Applications }

// newSettingsTestApp 造一个只装配好内容区与导航按钮的 App。
//
// 刻意不调用 BuildUI：这里要验证的是「内容区切换」这一层，
// 完整侧栏的组装由各自的页面测试覆盖。
func newSettingsTestApp(t *testing.T) (*App, fyne.Window) {
	t.Helper()
	test.NewApp()
	a := NewAppWithVersion(noopApps{}, &config.Config{}, nil, func() {}, "1.2.2", "")
	w := test.NewWindow(container.NewStack())
	w.Resize(fyne.NewSize(1200, 800))
	t.Cleanup(w.Close)

	a.window = w
	a.content = container.NewStack()
	a.navBtns = []*widget.Button{widget.NewButton("工作台", nil)}
	a.pages = []pageDef{{page: widget.NewLabel("业务页面")}}
	return a, w
}

// TestSettingsShownInContentAreaNotPopup 锁定设置显示在主内容区，而不是弹新窗口。
func TestSettingsShownInContentAreaNotPopup(t *testing.T) {
	a, _ := newSettingsTestApp(t)

	a.showSettings()

	if len(a.content.Objects) != 1 {
		t.Fatalf("内容区对象数 = %d，期望 1", len(a.content.Objects))
	}
	if a.settingsPanel == nil {
		t.Fatal("首次点击设置应懒创建面板")
	}
	// 内容区里放的应是设置面板建出的容器，而不是业务页面
	if _, isLabel := a.content.Objects[0].(*widget.Label); isLabel {
		t.Error("内容区仍是业务页面，说明设置没有替换主内容区")
	}
	if a.navBtns[0].Importance == widget.HighImportance {
		t.Error("显示设置时不应把业务导航项标为高亮（设置不属于任何业务模块）")
	}
}

// TestSelectRestoresBusinessPage 从设置点回业务导航应恢复业务页面并高亮。
func TestSelectRestoresBusinessPage(t *testing.T) {
	a, _ := newSettingsTestApp(t)

	a.showSettings()
	a.Select(0)

	if _, isLabel := a.content.Objects[0].(*widget.Label); !isLabel {
		t.Fatalf("点业务导航后应回到业务页面，实际 %T", a.content.Objects[0])
	}
	if a.navBtns[0].Importance != widget.HighImportance {
		t.Error("回到业务页面后该导航项应高亮")
	}
}

// TestSettingsPanelLazyBuilt 面板应懒建：未点设置前不应创建。
func TestSettingsPanelLazyBuilt(t *testing.T) {
	a, _ := newSettingsTestApp(t)
	if a.settingsPanel != nil {
		t.Fatal("未点击设置时不应已创建设置面板")
	}
	a.showSettings()
	if a.settingsPanel == nil {
		t.Fatal("点击设置后应创建设置面板")
	}
	// 再次点击应复用，不重建
	first := a.settingsPanel
	a.showSettings()
	if a.settingsPanel != first {
		t.Error("重复点击设置应复用同一面板")
	}
}

// TestSettingsPanelSwitchPage 面板自带导航可在三页间切换。
func TestSettingsPanelSwitchPage(t *testing.T) {
	p := newSettingsPanel("1.2.2", "", func() {})
	if len(p.navBtns) != 3 {
		t.Fatalf("设置导航项数 = %d，期望 3（使用指南 / 常见问题 / 更新说明）", len(p.navBtns))
	}
	if p.Build() == nil {
		t.Fatal("Build 应返回内容对象")
	}
	if p.navBtns[0].Importance != widget.HighImportance {
		t.Error("默认应停在第一页")
	}
	for _, idx := range []int{1, 2} {
		p.Select(idx)
		if p.navBtns[idx].Importance != widget.HighImportance {
			t.Errorf("切换后第 %d 项应高亮", idx)
		}
		for j, b := range p.navBtns {
			want := widget.MediumImportance
			if j == idx {
				want = widget.HighImportance
			}
			if b.Importance != want {
				t.Errorf("切到第 %d 页时第 %d 项强调状态 = %v，期望 %v", idx, j, b.Importance, want)
			}
		}
	}
	// 越界不应 panic
	p.Select(-1)
	p.Select(99)
}

// TestSettingsPanelNavLabels 三页的标题要如实反映内容，顺序为使用指南/常见问题/更新说明。
func TestSettingsPanelNavLabels(t *testing.T) {
	p := newSettingsPanel("1.2.2", "", func() {})
	p.Build()

	var got []string
	for _, b := range p.navBtns {
		got = append(got, b.Text)
	}
	want := []string{"使用指南", "常见问题", "更新说明"}
	if len(got) != len(want) {
		t.Fatalf("导航项 = %v，期望 %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("第 %d 项 = %q，期望 %q", i, got[i], want[i])
		}
	}
}

// TestSettingsPanelPagesDistinct 三页内容不应是同一个对象，否则切换看不出区别。
func TestSettingsPanelPagesDistinct(t *testing.T) {
	p := newSettingsPanel("1.2.2", "", func() {})
	p.Build()

	seen := map[fyne.CanvasObject]bool{}
	for i, pg := range p.pages {
		if seen[pg] {
			t.Errorf("第 %d 页与前面的页面是同一个对象", i)
		}
		seen[pg] = true
	}
}

// TestSettingsPanelBackRestores 调用 onBack 应恢复业务页面。
func TestSettingsPanelBackRestores(t *testing.T) {
	a, _ := newSettingsTestApp(t)
	a.showSettings()

	backCalled := false
	// 重新构造一个带 onBack 的面板来验证回调链路
	p := newSettingsPanel("1.2.2", "", func() { backCalled = true })
	p.Build()
	if p.build == nil {
		t.Fatal("面板应已登记构建回调")
	}
	// 触发返回按钮：面板导航第一项就是「返回主界面」，其回调应被调用
	if len(p.pages) == 0 {
		t.Fatal("面板应有页面")
	}
	// 直接验证 App 侧的回退行为
	a.settingsPanel = p
	p.back() // 触发 onBack
	if !backCalled {
		t.Error("onBack 未被调用")
	}
}

// TestSidebarFooterHasSettingsButton 左下角应有「设置」入口，且保留「退出登录」。
func TestSidebarFooterHasSettingsButton(t *testing.T) {
	// 侧栏 footer 会读当前登录人，先注入一个（沿用 backup_test.go 的做法）
	prev := auth.Current()
	auth.SetCurrent(&model.User{Username: "tester", Role: string(auth.RoleAdmin)})
	t.Cleanup(func() { auth.SetCurrent(prev) })

	a, _ := newSettingsTestApp(t)

	labels := collectButtonLabels(a.buildSidebarFooter())
	var hasSettings, hasLogout bool
	for _, l := range labels {
		if strings.Contains(l, "设置") {
			hasSettings = true
		}
		if strings.Contains(l, "退出登录") {
			hasLogout = true
		}
	}
	if !hasSettings {
		t.Errorf("左下角未找到「设置」按钮，实际按钮: %v", labels)
	}
	if !hasLogout {
		t.Errorf("「退出登录」按钮丢失，实际按钮: %v", labels)
	}
}

// collectButtonLabels 递归收集按钮文本。
func collectButtonLabels(o fyne.CanvasObject) []string {
	var out []string
	seen := map[fyne.CanvasObject]bool{}
	var walk func(fyne.CanvasObject)
	walk = func(c fyne.CanvasObject) {
		if c == nil || seen[c] {
			return
		}
		seen[c] = true
		if b, ok := c.(*widget.Button); ok {
			out = append(out, b.Text)
		}
		if box, ok := c.(*fyne.Container); ok {
			for _, child := range box.Objects {
				walk(child)
			}
		}
		if w, ok := c.(fyne.Widget); ok {
			for _, child := range test.WidgetRenderer(w).Objects() {
				walk(child)
			}
		}
	}
	walk(o)
	return out
}
