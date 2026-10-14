package api

import (
	"net/http"

	"app/internal/help"
)

// ---- 内置帮助内容 ----
//
// 「设置」屏的三页签（使用指南 / 常见问题 / 更新说明）都用这一份数据。
//
// 一个端点而不是三个：内容全部 go:embed 在二进制里，本机读内存，三份加起来
// 约 30KB，没有按页签拆请求的必要。拆开只会让前端多两处加载态与两处错误态，
// 而它们总是一起出现——打开设置屏就该有东西可显示，没有哪一页是可选加载的。
//
// 更新说明在这里就切成小节：复用 help.ParseNotes，与 Fyne 版 helpchanges.go
// 是同一个函数。前端不再写第二份「按 == 中文 == 切分」的逻辑，否则同一份
// notes 在两个界面上迟早切出不同的段落。
func (s *Server) handleHelp(w http.ResponseWriter, _ *http.Request) {
	entries, err := help.LoadChangelog()
	if err != nil {
		// 数据是编译期嵌入的，解析失败即打包坏了。不静默回空列表：
		// 那会让用户以为「没有历史版本」，把打包事故藏起来。
		writeServerErr(w, err)
		return
	}
	versions := make([]helpVersion, 0, len(entries))
	for _, e := range entries {
		versions = append(versions, helpVersion{
			Version:     e.Version,
			PublishedAt: e.PublishedAt,
			Sections:    helpSections(help.ParseNotes(e.Notes)),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		// 当前版本由进程自报（发布时 ldflags 注入），与 /api/v1/serverinfo 同源。
		// 界面**不要**改用前端写死的常量：搭出来的 exe 是什么版本，屏幕就该显示
		// 什么版本，否则升级后界面还在报旧号。
		"currentVersion": s.version,
		// 内置更新说明里记录的最新版本。断网时这是用户能看到的唯一「最新版本」。
		"latestVersion": help.LatestVersion(),
		// 两份 Markdown 原文，由前端按受限子集渲染（见 web/src/components/Markdown.tsx）。
		"guide": help.Guide(),
		"faq":   help.FAQ(),
		// 已切好小节的更新说明，按版本从新到旧。
		"versions": versions,
	})
}

// helpSection 是更新说明正文里的一小节。Title 为空表示这是正文前言。
type helpSection struct {
	Title string `json:"title,omitempty"`
	Body  string `json:"body"`
}

// helpVersion 是一个版本的更新说明。
type helpVersion struct {
	Version     string        `json:"version"`
	PublishedAt string        `json:"publishedAt,omitempty"`
	Sections    []helpSection `json:"sections"`
}

// helpSections 把 help 包的解析结果转成线协议结构。
//
// 不直接序列化 help.NotesSection：那一层是解析器的内部形状，
// 与线协议分开后，改动解析器不会静默改变客户端看到的字段。
func helpSections(in []help.NotesSection) []helpSection {
	out := make([]helpSection, 0, len(in))
	for _, sec := range in {
		out = append(out, helpSection{Title: sec.Title, Body: sec.Body})
	}
	return out
}
