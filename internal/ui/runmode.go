package ui

import (
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"app/internal/config"
)

// ShowRunModePicker 首启选择运行模式并持久化（D3）。
// local：单机直连；client：连接服务器。选完进入 onReady。
func ShowRunModePicker(a fyne.App, cfg *config.Config, onReady func(mode string)) {
	w := a.NewWindow("选择运行模式")
	w.Resize(fyne.NewSize(520, 300))
	w.CenterOnScreen()
	w.SetPadded(true)

	title := widget.NewLabelWithStyle("首次启动：请选择运行模式", fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
	hint := widget.NewLabel("此选择会保存到本机配置，之后启动不再询问。\n一般无需切换；如需更换请使用设置中的「切换运行模式」。")
	hint.Wrapping = fyne.TextWrapWord

	urlEntry := widget.NewEntry()
	urlEntry.SetPlaceHolder("服务器地址，例如 http://192.168.1.10:8080")
	urlRow := container.NewVBox(widget.NewLabel("服务器地址"), urlEntry)
	urlRow.Hide()

	localBtn := widget.NewButton("本机（单机使用）", func() {
		if err := cfg.SetRunMode(config.ModeLocal, ""); err != nil {
			dialog.ShowError(err, w)
			return
		}
		w.Close()
		onReady(config.ModeLocal)
	})

	clientBtn := widget.NewButton("连接服务器（车间多机）", func() {
		u := strings.TrimSpace(urlEntry.Text)
		if u == "" {
			urlRow.Show()
			dialog.ShowError(errNeedServerURL, w)
			return
		}
		if err := cfg.SetRunMode(config.ModeClient, u); err != nil {
			dialog.ShowError(err, w)
			return
		}
		w.Close()
		onReady(config.ModeClient)
	})

	box := container.NewVBox(
		title, hint, widget.NewSeparator(),
		localBtn, clientBtn, urlRow,
	)
	w.SetContent(container.NewPadded(box))
	w.Show()
}

type simpleErr string

func (e simpleErr) Error() string { return string(e) }

const errNeedServerURL = simpleErr("请填写服务器地址后再继续")
