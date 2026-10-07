package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"app/internal/help"
)

// faqPage 是「常见问题」页：只读正文，没有版本区与在线查询。
//
// 排版约束与使用指南一致，原因相同：应用只设了 FYNE_FONT（黑体，含中文），
// 没设 FYNE_FONT_MONOSPACE，等宽样式会退回 Fyne 内置的纯拉丁 Noto Mono，
// 中文会变成豆腐块 ◇。internal/help 的测试对两份文档一视同仁。
type faqPage struct {
	body *widget.RichText
}

func newFAQPage() fyne.CanvasObject {
	f := &faqPage{}
	f.body = widget.NewRichTextFromMarkdown(help.FAQ())
	f.body.Wrapping = fyne.TextWrapWord

	header := container.NewVBox(
		widget.NewLabelWithStyle("常见问题", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		widget.NewSeparator(),
	)
	return container.NewBorder(header, nil, nil, nil,
		container.NewVScroll(container.NewPadded(f.body)))
}
