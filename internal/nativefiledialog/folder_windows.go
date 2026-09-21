//go:build windows

package nativefiledialog

import (
	"syscall"
	"unsafe"
)

var (
	shell32                  = syscall.NewLazyDLL("shell32.dll")
	user32                   = syscall.NewLazyDLL("user32.dll")
	ole32                    = syscall.NewLazyDLL("ole32.dll")
	procSHBrowseForFolderW   = shell32.NewProc("SHBrowseForFolderW")
	procSHGetPathFromIDListW = shell32.NewProc("SHGetPathFromIDListW")
	procCoTaskMemFree        = ole32.NewProc("CoTaskMemFree")
	procCoInitializeEx       = ole32.NewProc("CoInitializeEx")
	procGetForegroundWindow  = user32.NewProc("GetForegroundWindow")
)

const (
	bifReturnOnlyFSDirs     = 0x0001
	bifNewDialogStyle       = 0x0040
	coinitApartmentThreaded = 0x2
)

type browseInfoW struct {
	hwndOwner      uintptr
	pidlRoot       uintptr
	pszDisplayName *uint16
	lpszTitle      *uint16
	ulFlags        uint32
	lpfn           uintptr
	lParam         uintptr
	iImage         int32
}

// PickFolder shows the native Windows folder picker (localized by the OS).
// It returns the selected directory and true, or ("", false) if cancelled.
func PickFolder(title string) (string, bool) {
	// Best-effort COM init on this thread; required by the modern dialog style.
	procCoInitializeEx.Call(0, coinitApartmentThreaded)

	titlePtr, err := syscall.UTF16PtrFromString(title)
	if err != nil {
		return "", false
	}

	display := make([]uint16, syscall.MAX_PATH)
	owner, _, _ := procGetForegroundWindow.Call()

	bi := browseInfoW{
		hwndOwner:      owner,
		pszDisplayName: &display[0],
		lpszTitle:      titlePtr,
		ulFlags:        bifReturnOnlyFSDirs | bifNewDialogStyle,
	}

	pidl, _, _ := procSHBrowseForFolderW.Call(uintptr(unsafe.Pointer(&bi)))
	if pidl == 0 {
		return "", false
	}
	defer procCoTaskMemFree.Call(pidl)

	var path [syscall.MAX_PATH]uint16
	ok, _, _ := procSHGetPathFromIDListW.Call(pidl, uintptr(unsafe.Pointer(&path[0])))
	if ok == 0 {
		return "", false
	}
	return syscall.UTF16ToString(path[:]), true
}
