//go:build windows

package winmsg

import (
	"syscall"
	"unsafe"
)

var (
	user32          = syscall.NewLazyDLL("user32.dll")
	procMessageBoxW = user32.NewProc("MessageBoxW")
	shell32         = syscall.NewLazyDLL("shell32.dll")
	procShellExec   = shell32.NewProc("ShellExecuteW")
)

const (
	mbOK          = 0x0
	mbOKCancel    = 0x1
	mbYesNoCancel = 0x3
	mbYesNo       = 0x4
	mbIconError   = 0x10
	mbIconQuest   = 0x20
	mbIconWarn    = 0x30
	mbIconInfo    = 0x40
)

const (
	idOK     = 1
	idCancel = 2
	idYes    = 6
	idNo     = 7
	swShow   = 5
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

// ConfirmOK 弹出「确定/取消」框，返回用户是否点了「确定」。
//
// 与 Confirm 的区别只在按钮文字：这里是「确定」而不是「是」。用在
// 「确定 = 去做一件明确的事（打开下载页）、取消 = 什么都不做」的场合，
// 「是/否」在那里的读法会别扭（"是否打开下载页"像在问要不要）。
func ConfirmOK(title, text string) bool {
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
		mbOKCancel|mbIconQuest,
	)
	return ret == idOK
}

// AskRetryOpenPage 弹「是 / 否 / 取消」，把三个按钮映射成三种意图。
//
// 按钮文字本身是「是/否/取消」，所以**文案必须写清每个按钮做什么**；
// 调用方负责把这句话写进去。返回 (retry, openPage)：都 false 表示用户
// 选了取消 —— 调用方应当安静退出，不要再弹第三个框。
func AskRetryOpenPage(title, text string) (retry bool, openPage bool) {
	t, err := syscall.UTF16PtrFromString(title)
	if err != nil {
		return false, false
	}
	m, err := syscall.UTF16PtrFromString(text)
	if err != nil {
		return false, false
	}
	ret, _, _ := procMessageBoxW.Call(
		0,
		uintptr(unsafe.Pointer(m)),
		uintptr(unsafe.Pointer(t)),
		mbYesNoCancel|mbIconWarn,
	)
	switch int64(ret) {
	case idYes:
		return true, false
	case idNo:
		return false, true
	default:
		return false, false
	}
}

// OpenURL 用系统默认浏览器打开一个网址。
//
// 存在的理由很具体：MessageBox 里的网址**是不可点击的纯文本**，而
// 「请手动下载」这类提示唯一可做的事就是去那个网址。让用户照着敲一条
// 70 字符的 URL 不算提示，算刁难。
func OpenURL(url string) error {
	verb, err := syscall.UTF16PtrFromString("open")
	if err != nil {
		return err
	}
	target, err := syscall.UTF16PtrFromString(url)
	if err != nil {
		return err
	}
	ret, _, callErr := procShellExec.Call(
		0,
		uintptr(unsafe.Pointer(verb)),
		uintptr(unsafe.Pointer(target)),
		0, 0, swShow,
	)
	// ShellExecuteW 的返回值 <= 32 才是失败；那些错误码不是 errno，
	// 所以不能只看 callErr。
	if ret <= 32 {
		return callErr
	}
	return nil
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
