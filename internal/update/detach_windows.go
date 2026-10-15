//go:build windows

package update

import (
	"syscall"

	"golang.org/x/sys/windows"
)

// detachedAttrs 让"换 exe 之后启动的新进程"**脱离**当前进程所在的作业对象。
//
// 为什么必须脱离：desktop-wails 在启动时把进程放进一个 KILL_ON_JOB_CLOSE 的作业
// （internal/windowsobj，用来保证 WebView2 那棵树随宿主一起消失）。而 Windows 的子
// 进程**默认继承父进程的作业** —— 更新是"老进程启动新进程、老进程随后退出"，
// 于是老进程一退，作业关闭就把刚起来的新版本一起杀掉。
//
// 2026-10-15 实机演练抓到过：新版本日志停在 `[WebView2] Environment created
// successfully`，然后无声消失，连"embedded server stopped"都没有（不是优雅退出，
// 是被内核终止）。当时旧版本是 Fyne 1.3.0（不建作业），所以只有"Wails 版升级
// Wails 版"才会踩到 —— 也就是**下一次更新**。
//
// 配合 internal/windowsobj 的 JOB_OBJECT_LIMIT_BREAKAWAY_OK 使用：那边只是"允许"
// 脱离，这里才是"要求"脱离。若作业没允许，CreateProcess 会失败并报出来，
// 而不是静默换掉却起不来。
func detachedAttrs() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: windows.CREATE_BREAKAWAY_FROM_JOB}
}
