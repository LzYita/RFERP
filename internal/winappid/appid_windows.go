//go:build windows

package winappid

import (
	"syscall"
	"unsafe"
)

var (
	shell32          = syscall.NewLazyDLL("shell32.dll")
	procSetProcessID = shell32.NewProc("SetCurrentProcessExplicitAppUserModelID")
)

// Set assigns an explicit AppUserModelID so Windows treats the process as a
// distinct app (fixes taskbar grouping/icon issues).
func Set(appID string) {
	p, err := syscall.UTF16PtrFromString(appID)
	if err != nil {
		return
	}
	procSetProcessID.Call(uintptr(unsafe.Pointer(p)))
}
