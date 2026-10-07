package help

import (
	"strings"
	"testing"

	"app/internal/update"
)

// TestGuideIsEmbedded 指南必须真的嵌进二进制，且有实际内容。
func TestGuideIsEmbedded(t *testing.T) {
	g := Guide()
	if strings.TrimSpace(g) == "" {
		t.Fatal("使用指南为空")
	}
	// 关键小节不应缺失，否则说明内容被误删
	for _, want := range []string{"开始使用", "备份与恢复", "权限一览"} {
		if !strings.Contains(g, want) {
			t.Errorf("使用指南缺少小节 %q", want)
		}
	}
}

// TestLoadChangelogEmbedded 校验内嵌更新说明能解析、顺序正确、内容真实。
func TestLoadChangelogEmbedded(t *testing.T) {
	entries, err := LoadChangelog()
	if err != nil {
		t.Fatalf("解析内嵌更新说明失败: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("内嵌更新说明为空")
	}
	for i, e := range entries {
		if e.Version == "" {
			t.Errorf("第 %d 条缺少版本号", i)
		}
		if strings.TrimSpace(e.Notes) == "" {
			t.Errorf("版本 %s 的说明为空", e.Version)
		}
	}
	// 必须按版本号从新到旧
	for i := 1; i < len(entries); i++ {
		prev, cur := entries[i-1].Version, entries[i].Version
		if compareVersionForTest(prev, cur) <= 0 {
			t.Errorf("顺序错误：%s 出现在 %s 之后", prev, cur)
		}
	}
}

// TestLatestVersion 是内嵌记录里的最高版本。
func TestLatestVersion(t *testing.T) {
	entries, err := LoadChangelog()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := LatestVersion(), entries[0].Version; got != want {
		t.Errorf("LatestVersion() = %q，期望 %q", got, want)
	}
}

// TestParseNotesBilingual 双语说明应按语言切开，并识别编号小节。
func TestParseNotesBilingual(t *testing.T) {
	notes := "RFERP v1.2.2\n\n== 中文 ==\n\n一、问题修复\n\n- 甲\n- 乙\n\n二、其他\n\n- 丙\n\n== English ==\n\nFixes\n\n- A\n\nOther\n\n- B\n"
	secs := ParseNotes(notes)
	if len(secs) < 4 {
		t.Fatalf("期望至少 4 个小节，实际 %d: %#v", len(secs), secs)
	}
	// 第一节应是中文的「一、问题修复」
	if secs[0].Title != "一、问题修复" {
		t.Errorf("第 1 节标题 = %q，期望 %q", secs[0].Title, "一、问题修复")
	}
	if !strings.Contains(secs[0].Body, "甲") || !strings.Contains(secs[0].Body, "乙") {
		t.Errorf("第 1 节正文缺少列表项: %q", secs[0].Body)
	}
	// 不应把英文语言块的内容混进中文块
	for _, s := range secs {
		if s.Title == "一、问题修复" && strings.Contains(s.Body, "- A") {
			t.Error("英文内容混进了中文小节")
		}
	}
}

// TestParseNotesNoLanguageMarker 没有语言分隔时应整体返回，不丢内容。
func TestParseNotesNoLanguageMarker(t *testing.T) {
	notes := "一、修复\n\n- 甲\n"
	secs := ParseNotes(notes)
	if len(secs) != 1 {
		t.Fatalf("期望 1 节，实际 %d", len(secs))
	}
	if !strings.Contains(secs[0].Body, "甲") {
		t.Errorf("内容丢失: %q", secs[0].Body)
	}
}

// TestParseNotesRealEmbeddedData 用内嵌的真实数据跑一遍，确保解析器跟得上实际格式。
func TestParseNotesRealEmbeddedData(t *testing.T) {
	entries, err := LoadChangelog()
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		secs := ParseNotes(e.Notes)
		if len(secs) == 0 {
			t.Errorf("版本 %s 解析出 0 个小节", e.Version)
		}
		// 解析后的内容总量不应显著少于原文（允许丢掉分隔行与空白）
		joined := 0
		for _, s := range secs {
			joined += len(s.Title) + len(s.Body)
		}
		if joined < len(e.Notes)/2 {
			t.Errorf("版本 %s 解析后内容明显偏少：原文 %d 字节 -> 解析 %d 字节",
				e.Version, len(e.Notes), joined)
		}
	}
}

// compareVersionForTest 便于在测试里断言顺序（复用生产实现）。
func compareVersionForTest(a, b string) int {
	return update.CompareVersions(a, b)
}
