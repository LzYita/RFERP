// Package help 提供随程序内置的离线帮助内容：使用指南与分版本更新说明。
//
// 两者都用 go:embed 打进二进制，因此断网也能查看——使用指南不该因为
// 没有网络就变成空白页。
//
// 数据来源与维护方式：
//   - guide.md      手工维护，面向软件使用者
//   - changelog.json 由 cmd/signmanifest 的 -changelog 参数在每次发布时累积，
//     不要手工编辑（否则下次发布会被覆盖）
package help

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"app/internal/update"
)

//go:embed guide.md
var guideMarkdown string

//go:embed faq.md
var faqMarkdown string

//go:embed changelog.json
var changelogJSON []byte

// Guide 返回使用指南的 Markdown 原文。
func Guide() string { return guideMarkdown }

// FAQ 返回常见问题的 Markdown 原文。
//
// 单独成页而不是并在使用指南末尾：常见问题是「按症状查找」的入口，
// 用户通常是带着一个具体问题来翻的，翻到指南末尾才看到并不符合使用习惯。
func FAQ() string { return faqMarkdown }

// Entry 是一个版本的更新说明。
type Entry struct {
	Version     string `json:"version"`
	PublishedAt string `json:"publishedAt,omitempty"`
	Notes       string `json:"notes"`
}

type changelogFile struct {
	Versions []Entry `json:"versions"`
}

// LoadChangelog 解析内嵌的更新说明，按版本号从新到旧排序。
//
// 数据文件在编译期就已嵌入，解析失败说明打包出了问题（例如文件被改坏），
// 因此返回错误而不是静默给出空列表——空列表会让用户以为"没有历史版本"。
func LoadChangelog() ([]Entry, error) {
	var f changelogFile
	if err := json.Unmarshal(changelogJSON, &f); err != nil {
		return nil, fmt.Errorf("解析内嵌更新说明失败: %w", err)
	}
	// 防御：剔除没有版本号的条目，否则界面上会出现无法点选的空行。
	out := make([]Entry, 0, len(f.Versions))
	for _, e := range f.Versions {
		if strings.TrimSpace(e.Version) == "" {
			continue
		}
		out = append(out, e)
	}
	// 文件本应已是新到旧；这里再排一次，保证调用方拿到的顺序可靠。
	// 复用 update.CompareVersions，避免版本号比较规则出现两份实现。
	sort.SliceStable(out, func(i, j int) bool {
		return update.CompareVersions(out[i].Version, out[j].Version) > 0
	})
	return out, nil
}

// LatestVersion 返回内嵌更新说明中的最新版本号；没有数据时返回空串。
func LatestVersion() string {
	entries, err := LoadChangelog()
	if err != nil || len(entries) == 0 {
		return ""
	}
	return entries[0].Version
}

// NotesSections 是从更新说明正文里切出的一段内容。
type NotesSection struct {
	// Title 为小节标题，例如「一、问题修复」；空表示这是正文前言。
	Title string
	// Body 为小节正文。
	Body string
}

// ParseNotes 解析双语更新说明正文。
//
// 线上发布说明的固定结构是：
//
//	RFERP v1.2.2
//
//	== 中文 ==
//
//	一、问题修复
//
//	- ...
//
//	== English ==
//
//	Fixes
//
//	- ...
//
// 所以这里按「== Xxx ==」切成若干小节；小节内以「一、」「二、」「1.」「2.」
// 这类编号行作为子标题。无法识别结构的文本原样返回，避免丢内容。
func ParseNotes(notes string) []NotesSection {
	lines := strings.Split(strings.ReplaceAll(notes, "\r\n", "\n"), "\n")

	// 定位所有语言分隔行
	type marker struct {
		idx   int
		label string
	}
	var markers []marker
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "==") && strings.HasSuffix(t, "==") && len(t) > 4 {
			markers = append(markers, marker{idx: i, label: strings.Trim(t, "= ")})
		}
	}
	if len(markers) == 0 {
		// 没有语言分隔，整体作为一节
		return []NotesSection{{Body: strings.TrimSpace(notes)}}
	}

	var out []NotesSection
	for k, m := range markers {
		end := len(lines)
		if k+1 < len(markers) {
			end = markers[k+1].idx
		}
		// 首行 "RFERP v1.2.2" 之类的抬头、末尾空行都去掉
		body := trimBlank(lines[m.idx+1 : end])
		if len(body) == 0 {
			continue
		}
		out = append(out, splitSubsections(body)...)
	}
	return out
}

// splitSubsections 把一节按编号行或独立短行再切成子小节。
func splitSubsections(body []string) []NotesSection {
	var out []NotesSection
	cur := NotesSection{}
	var curLines []string

	flush := func() {
		if len(curLines) == 0 {
			return
		}
		cur.Body = strings.TrimSpace(strings.Join(curLines, "\n"))
		out = append(out, cur)
		curLines = nil
		cur = NotesSection{}
	}

	for i, l := range body {
		if isSubheading(body, i) {
			flush()
			cur.Title = strings.TrimSpace(l)
			continue
		}
		curLines = append(curLines, l)
	}
	flush()
	if len(out) == 0 {
		return []NotesSection{{Body: strings.Join(body, "\n")}}
	}
	return out
}

// isSubheading 判断 body[i] 是否是小标题。
//
// 判据分两类：
//  1. 明确的编号行——「一、」「1.」「2)」「(1)」。这类无需看上下文。
//  2. 无编号的独立短行——英文说明里的小标题是裸单词（Fixes / Other /
//     Improvements / Highlights），中文里则一律带编号。这类行靠上下文判断：
//     自身短、不是列表项、**前面是空行**（即它开启了一个新块），
//     且后面紧跟空行或列表项。
//
// 「前面必须是空行」是关键：正文里的续行（列表项的换行续写）虽然也可能很短，
// 但它们紧跟在内容之后，不会被误判成标题。
func isSubheading(body []string, i int) bool {
	l := strings.TrimSpace(body[i])
	if l == "" {
		return false
	}
	r := []rune(l)
	if len(r) > 24 {
		return false
	}
	// 列表项永远不是标题
	if _, txt := listMarkerOf(l); txt != "" {
		return false
	}
	// 语言分隔行已在上一层处理掉
	if strings.HasPrefix(l, "==") {
		return false
	}

	// 明确的中文编号
	for _, p := range []string{"一、", "二、", "三、", "四、", "五、", "六、", "七、", "八、", "九、", "十、"} {
		if strings.HasPrefix(l, p) {
			return true
		}
	}
	// 明确的数字编号：1. / 1) / (1)
	if len(r) >= 3 {
		c := r[0]
		if (c >= '0' && c <= '9') || c == '(' {
			rest := string(r[1:])
			if strings.HasPrefix(rest, ". ") || strings.HasPrefix(rest, ") ") {
				return true
			}
		}
	}

	// 无编号短行：必须是块首
	prevBlank := i == 0 || strings.TrimSpace(body[i-1]) == ""
	if !prevBlank {
		return false
	}
	// 且后面紧跟空行或列表项
	if i+1 >= len(body) {
		return true
	}
	next := strings.TrimSpace(body[i+1])
	if next == "" {
		return true
	}
	_, txt := listMarkerOf(next)
	return txt != ""
}

// listMarkerOf 识别列表项，返回项目符号与正文；不是列表项则返回两个空串。
func listMarkerOf(l string) (marker, text string) {
	t := strings.TrimLeft(l, " \t")
	for _, p := range []string{"- ", "* ", "+ "} {
		if strings.HasPrefix(t, p) {
			return "•", strings.TrimSpace(t[2:])
		}
	}
	r := []rune(t)
	if len(r) >= 3 {
		c := r[0]
		if (c >= '0' && c <= '9') || c == '(' {
			rest := string(r[1:])
			if strings.HasPrefix(rest, ". ") || strings.HasPrefix(rest, ") ") {
				return string(r[:1]) + ".", strings.TrimSpace(string(r[2:]))
			}
		}
	}
	return "", ""
}

// trimBlank 去掉首尾空行。
func trimBlank(lines []string) []string {
	start, end := 0, len(lines)
	for start < end && strings.TrimSpace(lines[start]) == "" {
		start++
	}
	for end > start && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	return lines[start:end]
}
