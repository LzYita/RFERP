package main

import (
	"errors"
	"log"

	"app/internal/winmsg"
)

// 这个文件**故意不带 build tag**：它装的是被 main.go（同样不带 tag）引用的
// 平台无关部分。放进 webview2_windows.go 会导致 Linux 编译失败 ——
// CodeQL 的 Go 分析在 Linux 上跑，正是它先发现了这一点。

// errWebView2Unavailable 表示用户选择了放弃（取消 / 退出）。
// 调用方据此安静退出：该说的话 offerManualInstall 都已经说过了。
var errWebView2Unavailable = errors.New("webview2 不可用，用户已选择退出")

// offerManualInstall 是"取消自动安装"之后的唯一出路：说明 + 打开下载页。
//
// 为什么是「确定/取消」而不是把网址印在正文里：MessageBox 里的网址点不动，
// 让用户照着敲一条 70 字符的 URL 不算提示。按钮语义因此与正文一一对应。
func offerManualInstall() error {
	if winmsg.ConfirmOK(webviewMessages().MissingRequirements, webviewCancelNotice) {
		return openDownloadPage()
	}
	return errWebView2Unavailable
}

// openDownloadPage 打开下载页；打不开就再给一次纯文本指引（此时没有别的办法）。
func openDownloadPage() error {
	if err := winmsg.OpenURL(webviewDownloadPage); err != nil {
		log.Printf("webview2: 打开下载页失败: %v", err)
		winmsg.Error("无法打开浏览器", "请手动在浏览器里打开：\n"+webviewDownloadPage)
	}
	return errWebView2Unavailable
}
