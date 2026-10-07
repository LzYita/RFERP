package help

import (
	"strings"
	"testing"

	"app/internal/auth"
)

// roleLabels 是系统的四个角色，label 必须与 auth 里的角色语义一致。
var roleLabels = []struct {
	role  auth.Role
	label string
}{
	{auth.RoleAdmin, "管理员"},
	{auth.RoleWarehouse, "仓管"},
	{auth.RoleProduction, "生产"},
	{auth.RoleViewer, "只读"},
}

// moduleLabels 把权限模块的代码键映射到指南里显示的中文名。
var moduleLabels = map[string]string{
	auth.ModuleDashboard: "工作台",
	auth.ModuleStats:     "统计分析",
	auth.ModuleProducts:  "产品管理",
	auth.ModuleParts:     "零件管理",
	auth.ModuleBOM:       "BOM管理",
	auth.ModuleBatch:     "批次追溯",
	auth.ModuleAudit:     "操作记录",
	auth.ModuleBackup:    "备份导出",
	auth.ModuleUsers:     "用户管理",
}

// grant 是指南里「某角色在某模块上的权限」的一条记录。
type grant struct {
	role  string
	level string
}

// levelWord 把 auth 的权限档位翻译成指南里写的字。
func levelWord(a auth.Access) string {
	switch a {
	case auth.AccessWrite:
		return "写"
	case auth.AccessRead:
		return "读"
	default:
		return "无"
	}
}

// parseModuleLine 解析指南里的权限行，例如：
//
//   - **备份导出** —— 管理员 可写；仓管 只读；生产、只读 无权限
//
// 要求指南对每个模块都点名全部角色，不使用「其余角色」这类模糊说法，
// 否则读者无法判断自己的角色属于哪一档。
func parseModuleLine(t *testing.T, module string, line string) map[string]string {
	t.Helper()
	out := map[string]string{}

	body := line
	if i := strings.Index(body, "——"); i >= 0 {
		body = body[i+len("——"):]
	} else {
		t.Fatalf("%s 的权限行缺少「——」分隔：%s", module, line)
	}

	for _, seg := range strings.Split(body, "；") {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		var level string
		for _, lv := range []string{"可写", "只读", "无权限"} {
			if strings.HasSuffix(seg, lv) {
				level = lv
				break
			}
		}
		if level == "" {
			t.Fatalf("%s 的权限段无法识别权限档位：%q", module, seg)
		}
		names := strings.TrimSpace(strings.TrimSuffix(seg, level))
		short := map[string]string{"可写": "写", "只读": "读", "无权限": "无"}[level]
		// 权限表必须逐个点名角色，否则读者无法判断自己属于哪一档。
		for _, vague := range []string{"其余", "全部", "均为", "其他", "其它"} {
			if strings.Contains(seg, vague) {
				t.Errorf("%s 的权限段用了模糊说法 %q，应逐个点名角色：%s", module, vague, seg)
			}
		}
		for _, n := range strings.Split(names, "、") {
			n = strings.TrimSpace(n)
			if n == "" {
				continue
			}
			if _, dup := out[n]; dup {
				t.Errorf("%s 的权限行里角色 %q 出现多次", module, n)
			}
			out[n] = short
		}
	}
	return out
}

// moduleLine 找出指南里描述该模块权限的那一行。
func moduleLine(module string) string {
	label := moduleLabels[module]
	for _, l := range strings.Split(strings.ReplaceAll(Guide(), "\r\n", "\n"), "\n") {
		if strings.Contains(l, "**"+label+"**") && strings.Contains(l, "——") {
			return l
		}
	}
	return ""
}

// TestGuidePermissionsMatchCode 逐模块、逐角色校验指南与代码一致。
//
// 这是本次修复的重点：指南里的权限表曾经与代码脱节，
// 而 Markdown 表格/代码块在 Fyne 下还会导致中文乱码与排版重叠，
// 所以这里逐行解析列表形式并与 auth.AccessFor 比对。
func TestGuidePermissionsMatchCode(t *testing.T) {
	for module := range moduleLabels {
		line := moduleLine(module)
		if line == "" {
			t.Errorf("指南里找不到模块 %s（%s）的权限行", module, moduleLabels[module])
			continue
		}
		grants := parseModuleLine(t, module, line)

		// 指南必须点名全部四个角色
		for _, r := range roleLabels {
			if _, ok := grants[r.label]; !ok {
				t.Errorf("模块 %s 的权限行漏掉了角色 %q：%s", module, r.label, line)
			}
		}
		// 不得出现不存在的角色名
		for name := range grants {
			if !knownRole(name) {
				t.Errorf("模块 %s 的权限行提到未知角色 %q：%s", module, name, line)
			}
		}
		// 逐角色与代码比对
		for _, r := range roleLabels {
			got, ok := grants[r.label]
			if !ok {
				continue
			}
			want := levelWord(auth.AccessFor(r.role, module))
			if got != want {
				t.Errorf("模块 %s 角色 %s：指南写 %q，代码是 %q（%s）",
					module, r.label, got, want, line)
			}
		}
	}
}

// TestGuideMentionsEveryRole 指南必须介绍全部角色，且不能用不存在的角色名。
func TestGuideMentionsEveryRole(t *testing.T) {
	guide := Guide()
	for _, r := range roleLabels {
		if !strings.Contains(guide, r.label) {
			t.Errorf("指南未提到角色 %q", r.label)
		}
	}
	for _, fake := range []string{"普通用户", "超级管理员", "访客", "guest"} {
		if strings.Contains(guide, fake) {
			t.Errorf("指南提到不存在的角色 %q；系统只有管理员/仓管/生产/只读", fake)
		}
	}
}

// TestGuideAvoidsCodeBlockAndQuote 使用指南与常见问题都不得使用代码块与引用块。
//
// 两者都会让中文变乱码：Fyne 的代码块使用等宽字体，而应用只设了 FYNE_FONT
// （黑体，含中文），没设 FYNE_FONT_MONOSPACE，等宽样式会退回 Fyne 内置的
// Noto Mono——纯拉丁字体，代码块里的中文会渲染成豆腐块 ◇；
// 引用块还会因缩进在窄面板里溢出，造成排版混乱。
// 这些问题 headless 测试都发现不了——那里根本没装 CJK 字体，
// 所以用静态检查把它们挡在门外。
func TestGuideAvoidsCodeBlockAndQuote(t *testing.T) {
	checkNoCodeBlockOrQuote(t, "guide.md", Guide())
	checkNoCodeBlockOrQuote(t, "faq.md", FAQ())
}

func checkNoCodeBlockOrQuote(t *testing.T, name, doc string) {
	t.Helper()
	for i, l := range strings.Split(strings.ReplaceAll(doc, "\r\n", "\n"), "\n") {
		tl := strings.TrimSpace(l)
		if strings.HasPrefix(tl, "```") {
			t.Errorf("%s 第 %d 行使用了代码块：中文在等宽字体下会变乱码 -> %s", name, i+1, tl)
		}
		if strings.HasPrefix(tl, ">") {
			t.Errorf("%s 第 %d 行使用了引用块：缩进会导致溢出/重叠 -> %s", name, i+1, tl)
		}
	}
}

// TestGuideMonospaceSpansAreASCII 行内代码必须只含 ASCII。
//
// 等宽样式会退回 Fyne 内置的 Noto Mono（纯拉丁），因此任何走等宽样式的中文
// 都会渲染成豆腐块 ◇。代码块已由 TestGuideAvoidsCodeBlockAndQuote 挡住，
// 这里补上行内代码这一种。
func TestGuideMonospaceSpansAreASCII(t *testing.T) {
	checkMonospaceSpansASCII(t, "guide.md", Guide())
	checkMonospaceSpansASCII(t, "faq.md", FAQ())
}

func checkMonospaceSpansASCII(t *testing.T, name, doc string) {
	t.Helper()
	lines := strings.Split(strings.ReplaceAll(doc, "\r\n", "\n"), "\n")
	inSpan := false
	start := 0
	cur := ""
	for i, l := range lines {
		parts := strings.Split(l, "`")
		if len(parts) == 1 {
			continue
		}
		for k := 1; k < len(parts); k++ {
			if !inSpan {
				inSpan, cur, start = true, parts[k], i+1
				continue
			}
			inSpan = false
			if strings.TrimSpace(cur) == "" {
				continue
			}
			for _, r := range cur {
				if r > 0x7F {
					t.Errorf("%s 第 %d 行的行内代码含非 ASCII 字符 %q（会渲染成豆腐块）：%s",
						name, start, r, cur)
					break
				}
			}
		}
	}
	if inSpan && strings.TrimSpace(cur) != "" {
		t.Errorf("%s 第 %d 行的行内代码没有闭合：%s", name, start, cur)
	}
}

// TestGuideHasNoMarkdownTables Fyne 的 markdown 渲染器不支持表格，
// 表格会显示成一串竖线。
func TestGuideHasNoMarkdownTables(t *testing.T) {
	for _, d := range []struct{ name, doc string }{
		{"guide.md", Guide()}, {"faq.md", FAQ()},
	} {
		for i, l := range strings.Split(strings.ReplaceAll(d.doc, "\r\n", "\n"), "\n") {
			tl := strings.TrimSpace(l)
			if strings.HasPrefix(tl, "|") && strings.Count(tl, "|") >= 2 {
				t.Errorf("%s 第 %d 行仍是 markdown 表格，Fyne 渲染不出来：%s", d.name, i+1, tl)
			}
		}
	}
}

// TestFAQIsEmbedded 常见问题必须已内置，且有实际内容。
func TestFAQIsEmbedded(t *testing.T) {
	if strings.TrimSpace(FAQ()) == "" {
		t.Fatal("常见问题内容为空")
	}
	if !strings.Contains(FAQ(), "# 常见问题") {
		t.Error("常见问题缺少一级标题")
	}
}

// TestFAQNotDuplicatedInGuide 常见问题已独立成页，使用指南里不该再留一份。
//
// 两处各留一份的后果是改一边忘了另一边，用户会看到互相矛盾的说法。
func TestFAQNotDuplicatedInGuide(t *testing.T) {
	if strings.Contains(Guide(), "常见问题") {
		t.Error("使用指南里仍出现「常见问题」，该内容已独立成页，请从 guide.md 移除")
	}
	// 抽查几个问答，确认确实是从指南搬过去的、而不是新写的
	for _, q := range []string{"数据库不可用", "忘记密码", "库存数字看起来不对", "结果不确定"} {
		if !strings.Contains(FAQ(), q) {
			t.Errorf("常见问题缺少问答「%s」，搬迁时可能丢内容", q)
		}
	}
}

// TestDocsHaveNoSharedQuestion 使用指南与常见问题不应重复同一条问答。
func TestDocsHaveNoSharedQuestion(t *testing.T) {
	qs := questionsIn(FAQ())
	if len(qs) == 0 {
		t.Fatal("常见问题里没有识别到任何问答，格式可能变了")
	}
	for _, q := range qs {
		if strings.Contains(Guide(), q) {
			t.Errorf("问答「%s」同时出现在使用指南与常见问题里，应只保留一处", q)
		}
	}
}

// questionsIn 抽出文档里所有以 **Q： 开头的问句。
func questionsIn(doc string) []string {
	var out []string
	for _, l := range strings.Split(strings.ReplaceAll(doc, "\r\n", "\n"), "\n") {
		tl := strings.TrimSpace(l)
		if strings.HasPrefix(tl, "**Q：") {
			out = append(out, strings.TrimSuffix(strings.TrimPrefix(tl, "**"), "**"))
		}
	}
	return out
}

// TestGuideValidUTF8 两份文档都必须是合法 UTF-8 且无 BOM，否则渲染成乱码。
func TestGuideValidUTF8(t *testing.T) {
	for _, d := range []struct{ name, doc string }{
		{"guide.md", guideMarkdown}, {"faq.md", faqMarkdown},
	} {
		if strings.HasPrefix(d.doc, "\uFEFF") {
			t.Errorf("%s 开头有 UTF-8 BOM，Fyne 渲染会把 BOM 显示成乱码字符", d.name)
		}
		if strings.ContainsRune(d.doc, '\uFFFD') {
			t.Errorf("%s 含有 U+FFFD 替换字符，说明内容已损坏", d.name)
		}
		if !strings.Contains(d.doc, "的") {
			t.Errorf("%s 读不出中文，编码可能不是 UTF-8", d.name)
		}
	}
}

func knownRole(name string) bool {
	for _, r := range roleLabels {
		if r.label == name {
			return true
		}
	}
	return false
}
