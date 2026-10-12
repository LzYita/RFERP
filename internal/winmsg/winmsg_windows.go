//go:build windows

package winmsg

import (
	"syscall"
	"unsafe"
)

var (
	user32          = syscall.NewLazyDLL("user32.dll")
	procMessageBoxW = user32.NewProc("MessageBoxW")
)

const (
	mbOK        = 0x0
	mbYesNo     = 0x4
	mbIconError = 0x10
	mbIconInfo  = 0x40
	mbIconQuest = 0x20
)

const (
	idYes = 6
	idNo  = 7
)

func Error(title, text string) {
	messageBox(title, text, mbOK|mbIconError)
}

func Info(title, text string) {
	messageBox(title, text, mbOK|mbIconInfo)
}

// Confirm 弹出「是/否」询问框，返回用户是否点了「是」。
//
// 用于「会改动用户本地文件」这类不可逆动作前的确认——软件更新替换
// 正在运行的 exe 就属于这一类，必须让用户明确点头。
func Confirm(title, text string) bool {
	t, err := syscall.UTF16PtrFromString(title)
	if err != nil {
		return false
	}
	m, err := syscall.UTF16PtrFromString(text)
	if err != nil {
		return false
	}
	ret, _, _ := procMessageBoxW.Call(
		0,
		uintptr(unsafe.Pointer(m)),
		uintptr(unsafe.Pointer(t)),
		mbYesNo|mbIconQuest,
	)
	return ret == idYes
}

func messageBox(title, text string, flags uintptr) {
	t, err := syscall.UTF16PtrFromString(title)
	if err != nil {
		return
	}
	m, err := syscall.UTF16PtrFromString(text)
	if err != nil {
		return
	}
	procMessageBoxW.Call(0, uintptr(unsafe.Pointer(m)), uintptr(unsafe.Pointer(t)), flags)
}
