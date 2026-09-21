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
	mbIconError = 0x10
	mbIconInfo  = 0x40
)

func Error(title, text string) {
	messageBox(title, text, mbOK|mbIconError)
}

func Info(title, text string) {
	messageBox(title, text, mbOK|mbIconInfo)
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
