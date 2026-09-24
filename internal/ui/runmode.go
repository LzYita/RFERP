package ui

import (
	"fmt"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"app/internal/config"
	"app/internal/usecase"
)

// ShowRunModePicker 首启选择运行模式并持久化（D3）。
// local：单机直连；client：连接服务器。选完进入 onReady。
func ShowRunModePicker(a fyne.App, cfg *config.Config, onReady func(mode string)) {
	w := a.NewWindow("选择运行模式")
	w.Resize(fyne.NewSize(520, 300))
	w.CenterOnScreen()
	w.SetPadded(true)

	title := widget.NewLabelWithStyle("首次启动：请选择运行模式", fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
	hint := widget.NewLabel("此选择会保存到本机配置，之后启动不再询问。")
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
		// 先打开下一界面再关本窗，避免 Fyne 因最后一个窗口关闭而退出。
		onReady(config.ModeLocal)
		w.Close()
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
		onReady(config.ModeClient)
		w.Close()
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

// ShowRunModeSwitch 低频入口：切换运行模式（D3）。不迁移数据，确认后写配置并提示重启。
func ShowRunModeSwitch(apps usecase.Applications, cfg *config.Config, parent fyne.Window) {
	if cfg == nil {
		dialog.ShowInformation("提示", "当前无法修改运行模式", parent)
		return
	}
	dialog.ShowConfirm("切换运行模式",
		"切换后不会自动迁移数据：\n本机库与服务器库是两套独立数据。\n\n需要退出并重新启动应用。继续？",
		func(ok bool) {
			if !ok {
				return
			}
			a := fyne.CurrentApp()
			ShowRunModePicker(a, cfg, func(mode string) {
				dialog.ShowInformation("已保存",
					fmt.Sprintf("运行模式已切换为 %s。\n请退出并重新启动 RFERP。", mode),
					parent)
			})
		}, parent)
}
