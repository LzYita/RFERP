//go:build windows

package nativefiledialog

import (
	"path/filepath"
	"syscall"
	"unicode/utf16"
	"unsafe"
)

var (
	comdlg32             = syscall.NewLazyDLL("comdlg32.dll")
	procGetOpenFileNameW = comdlg32.NewProc("GetOpenFileNameW")
)

const (
	ofnReadOnly      = 0x00000001
	ofnNoChangeDir   = 0x00000008
	ofnPathMustExist = 0x00000800
	ofnFileMustExist = 0x00001000
	ofnExplorer      = 0x00080000
)

type openFileNameW struct {
	lStructSize       uint32
	hwndOwner         uintptr
	hInstance         uintptr
	lpstrFilter       *uint16
	lpstrCustomFilter *uint16
	nMaxCustFilter    uint32
	nFilterIndex      uint32
	lpstrFile         *uint16
	nMaxFile          uint32
	lpstrFileTitle    *uint16
	nMaxFileTitle     uint32
	lpstrInitialDir   *uint16
	lpstrTitle        *uint16
	flags             uint32
	nFileOffset       uint16
	nFileExtension    uint16
	lpstrDefExt       *uint16
	lCustData         uintptr
	lpfnHook          uintptr
	lpTemplateName    *uint16
	pvReserved        unsafe.Pointer
	dwReserved        uint32
	flagsEx           uint32
}

// PickFile 弹出 Windows 原生的"打开文件"对话框，返回选中的文件。
//
// 取消或任何错误都返回 ("", false)：调用方只需要区分"选了一个文件"和
// "没选"，错误详情对用户没有额外价值（对话框自己已经显示过）。
func PickFile(title, defExt string) (string, bool) {
	titlePtr, err := syscall.UTF16PtrFromString(title)
	if err != nil {
		return "", false
	}
	extPtr, err := syscall.UTF16PtrFromString(defExt)
	if err != nil {
		return "", false
	}

	// 过滤器格式：说明\0模式\0说明\0模式\0\0，每段都以 NUL 结尾。
	// 用 utf16.Encode 手工拼，不能走 UTF16PtrFromString（它拒绝内含 NUL 的串）。
	var filter []uint16
	add := func(s string) {
		filter = append(filter, utf16.Encode([]rune(s))...)
		filter = append(filter, 0)
	}
	label := "可执行文件 (*." + defExt + ")"
	pattern := "*." + defExt
	add(defExt)
	add(pattern)
	add(label)
	add(pattern)
	filter = append(filter, 0)

	buf := make([]uint16, 32768) // 长路径 + 多选的余量
	owner, _, _ := procGetForegroundWindow.Call()

	ofn := openFileNameW{
		lStructSize: uint32(unsafe.Sizeof(openFileNameW{})),
		hwndOwner:   owner,
		lpstrFilter: &filter[0],
		nFilterIndex: 1,
		lpstrFile:   &buf[0],
		nMaxFile:    uint32(len(buf)),
		lpstrTitle:  titlePtr,
		lpstrDefExt: extPtr,
		flags:       ofnExplorer | ofnFileMustExist | ofnPathMustExist | ofnNoChangeDir,
	}
	ok, _, _ := procGetOpenFileNameW.Call(uintptr(unsafe.Pointer(&ofn)))
	if ok == 0 {
		return "", false
	}
	path := syscall.UTF16ToString(buf)
	if path == "" {
		return "", false
	}
	return filepath.Clean(path), true
}
