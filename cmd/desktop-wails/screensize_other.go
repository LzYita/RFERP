//go:build !windows

package main

import "app/internal/winmsg"

// fitWindow 非 Windows 平台没有工作区概念，按期望值返回。
func fitWindow(w, h int) (int, int) { return w, h }

// fitTo 与 Windows 版保持同样的签名与语义，便于共用测试。
func fitTo(workW, workH, wantW, wantH int) (int, int) {
	if wantW > workW {
		wantW = workW
	}
	if wantH > workH {
		wantH = workH
	}
	return wantW, wantH
}

// workArea 非 Windows 平台没有工作区概念，返回一个足够大的值。
func workArea() (int, int) { return 1920, 1080 }

func showStartupError(title, msg string) { winmsg.Error(title, msg) }
