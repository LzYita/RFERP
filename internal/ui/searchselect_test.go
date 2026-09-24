package ui

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"app/internal/model"
)

func TestContainsFold(t *testing.T) {
	cases := []struct {
		hay, needle string
		want        bool
	}{
		{"M003 电源线 5V", "电源", true},
		{"M003 电源线 5V", "m003", true},
		{"M003 电源线 5V", "5v", true},
		{"M003 电源线 5V", "塑料", false},
		{"M003 电源线 5V", "  ", true}, // 空关键字视为不过滤
	}
	for _, c := range cases {
		if got := containsFold(c.hay, c.needle); got != c.want {
			t.Errorf("containsFold(%q, %q) = %v, want %v", c.hay, c.needle, got, c.want)
		}
	}
}

func TestPartOptionsIncludeCodeNameAndSpec(t *testing.T) {
	spec := "5V"
	opts := partOptions([]model.Part{
		{ID: 1, Code: "M003", Name: "电源线", Spec: &spec},
		{ID: 2, Code: "M005", Name: "塑料外壳"},
	})
	if len(opts) != 2 {
		t.Fatalf("options = %d, want 2", len(opts))
	}
	if opts[0].Label != "M003  电源线  5V" {
		t.Errorf("label = %q", opts[0].Label)
	}
	if !containsFold(opts[0].Text, "5V") || !containsFold(opts[1].Text, "塑料") {
		t.Errorf("text 未包含可搜索字段: %q / %q", opts[0].Text, opts[1].Text)
	}
}

func TestSearchSelectFiltersByCodeNameAndSpec(t *testing.T) {
	test.NewApp()

	s := newSearchSelect([]searchOption{
		{ID: 1, Label: "M003  电源线  5V", Text: "M003 电源线 5V"},
		{ID: 2, Label: "M005  塑料外壳", Text: "M005 塑料外壳"},
		{ID: 3, Label: "P001  智能电水壶", Text: "P001 智能电水壶"},
	}, "搜索")

	if len(s.matches) != 3 {
		t.Fatalf("初始应显示全部，got %d", len(s.matches))
	}

	s.entry.SetText("电源") // 按名称
	if len(s.matches) != 1 || s.matches[0].ID != 1 {
		t.Fatalf("按名称筛选失败: %+v", s.matches)
	}

	s.entry.SetText("m00") // 按编码，大小写无关
	if len(s.matches) != 2 {
		t.Fatalf("按编码筛选应为 2，got %d", len(s.matches))
	}

	s.entry.SetText("5v") // 按规格，大小写无关
	if len(s.matches) != 1 || s.matches[0].ID != 1 {
		t.Fatalf("按规格筛选失败: %+v", s.matches)
	}

	s.entry.SetText("不存在的零件")
	if len(s.matches) != 0 {
		t.Fatalf("无匹配时应为 0，got %d", len(s.matches))
	}
}

func TestSearchSelectValue(t *testing.T) {
	test.NewApp()

	s := newSearchSelect(partOptions([]model.Part{
		{ID: 7, Code: "M003", Name: "电源线"},
		{ID: 8, Code: "M005", Name: "塑料外壳"},
	}), "搜索")

	// 未输入 → 未选中
	if _, ok := s.value(); ok {
		t.Fatal("空输入不应有值")
	}

	// 筛选结果唯一 → 自动采用（兼容直接输入）
	s.entry.SetText("电源")
	if got, ok := s.value(); !ok || got.ID != 7 {
		t.Fatalf("唯一匹配应自动采用，got %+v, %v", got, ok)
	}

	// 点击选中
	s.filter("")
	s.selected = &s.matches[1]
	if got, ok := s.value(); !ok || got.ID != 8 {
		t.Fatalf("点选后应返回所选，got %+v, %v", got, ok)
	}

	// setOptions 应清空输入与选择
	s.setOptions(partOptions([]model.Part{{ID: 9, Code: "X1", Name: "新零件"}}))
	if _, ok := s.value(); ok {
		t.Fatal("setOptions 后应清空选择")
	}
	if len(s.matches) != 1 || s.matches[0].ID != 9 {
		t.Fatalf("setOptions 未更新候选: %+v", s.matches)
	}
}

func TestSearchSelectDropdownAppearsOnTyping(t *testing.T) {
	test.NewApp()

	parts := partOptions([]model.Part{
		{ID: 1, Code: "M003", Name: "电源线"},
		{ID: 2, Code: "M005", Name: "塑料外壳"},
	})
	s := newSearchSelect(parts, "搜索")
	w := test.NewWindow(s.object())
	w.Resize(fyne.NewSize(600, 400))
	defer w.Close()

	if s.popUp != nil && s.popUp.Visible() {
		t.Fatal("初始不应弹出下拉（不占位）")
	}

	s.entry.SetText("电源")
	if s.popUp == nil || !s.popUp.Visible() {
		t.Fatal("输入关键字后应弹出下拉浮层")
	}

	s.entry.SetText("不存在的零件")
	if s.popUp != nil && s.popUp.Visible() {
		t.Fatal("无匹配时不应弹出下拉")
	}

	s.entry.SetText("塑料")
	if s.popUp == nil || !s.popUp.Visible() {
		t.Fatal("再次输入应重新弹出")
	}

	s.entry.SetText("")
	if s.popUp != nil && s.popUp.Visible() {
		t.Fatal("清空输入后应收起")
	}

	s.entry.SetText("电源")
	s.setOptions(parts)
	if s.popUp != nil && s.popUp.Visible() {
		t.Fatal("setOptions 后应收起")
	}
}
