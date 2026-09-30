package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"app/internal/help"
)

// changelogPage 是「更新说明」页：左侧版本列表可点击切换，右侧显示该版本说明。
//
// 构造与构建分开（newChangelogPage / build），这样测试可以直接拿到页对象
// 验证切换行为，而不必从容器树里反查。
type changelogPage struct {
	entries []help.Entry
	err     error
	list    *widget.List
	detail  *widget.RichText
}

func newChangelogPage() *changelogPage {
	entries, err := help.LoadChangelog()
	return &changelogPage{entries: entries, err: err}
}

// build 组装界面。
func (p *changelogPage) build() fyne.CanvasObject {
	title := widget.NewLabelWithStyle("分版本更新说明", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	if p.err != nil {
		return container.NewVBox(title, widget.NewLabel("无法读取内置更新说明："+p.err.Error()))
	}
	if len(p.entries) == 0 {
		return container.NewVBox(title, widget.NewLabel("暂无历史版本记录。"))
	}

	p.list = widget.NewList(
		func() int { return len(p.entries) },
		func() fyne.CanvasObject { return widget.NewLabel("template") },
		func(id widget.ListItemID, o fyne.CanvasObject) {
			if id < 0 || id >= len(p.entries) {
				return
			}
			o.(*widget.Label).SetText(p.listItemText(id))
		},
	)
	p.list.OnSelected = func(id widget.ListItemID) {
		p.show(id)
		// 右侧内容已经体现了当前版本，列表本身不高亮，避免两处都亮像是重复选择
		p.list.Unselect(id)
	}

	p.detail = widget.NewRichText()
	p.detail.Wrapping = fyne.TextWrapWord

	split := container.NewHSplit(
		container.NewPadded(p.list),
		container.NewVScroll(container.NewPadded(p.detail)),
	)
	split.Offset = 0.22

	p.show(0)
	return container.NewBorder(
		widget.NewLabelWithStyle("分版本更新说明（点击左侧版本切换）", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		nil, nil, nil, split,
	)
}

// listItemText 版本列表项的显示文本。
func (p *changelogPage) listItemText(id int) string {
	return "v" + p.entries[id].Version
}

// show 显示第 id 个版本的说明。
func (p *changelogPage) show(id int) {
	if id < 0 || id >= len(p.entries) || p.detail == nil {
		return
	}
	e := p.entries[id]

	var segs []widget.RichTextSegment
	segs = append(segs, &widget.TextSegment{
		Text:  "v" + e.Version,
		Style: widget.RichTextStyle{TextStyle: fyne.TextStyle{Bold: true}},
	})
	if date := formatReleaseDate(e.PublishedAt); date != "" {
		segs = append(segs, &widget.TextSegment{
			Text:  "    发布于 " + date,
			Style: widget.RichTextStyle{TextStyle: fyne.TextStyle{Italic: true}},
		})
	}
	segs = append(segs, &widget.TextSegment{Text: ""})

	for _, sec := range help.ParseNotes(e.Notes) {
		if sec.Title != "" {
			segs = append(segs, &widget.TextSegment{
				Text:  sec.Title,
				Style: widget.RichTextStyle{TextStyle: fyne.TextStyle{Bold: true}},
			})
		}
		if sec.Body != "" {
			segs = append(segs, &widget.TextSegment{Text: sec.Body})
		}
		segs = append(segs, &widget.TextSegment{Text: ""})
	}

	p.detail.Segments = segs
	p.detail.Refresh()
}

// formatReleaseDate 把 RFC3339 时间戳截成日期部分；格式不符时原样返回。
func formatReleaseDate(ts string) string {
	if len(ts) >= 10 && ts[4] == '-' && ts[7] == '-' {
		return ts[:10]
	}
	return ts
}
