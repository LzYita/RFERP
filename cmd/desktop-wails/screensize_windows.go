//go:build windows

package main

import (
	"syscall"
	"unsafe"

	"app/internal/winmsg"
)

var (
	user32                  = syscall.NewLazyDLL("user32.dll")
	procGetSystemMetrics    = user32.NewProc("GetSystemMetrics")
	procSystemParametersGet = user32.NewProc("SystemParametersInfoW")
)

const (
	smCXScreen     = 0
	smCYScreen     = 1
	spiGetWorkArea = 0x0030
)

// fitWindow 把期望尺寸收敛到当前屏幕的工作区内。
func fitWindow(wantW, wantH int) (int, int) {
	w, h := workArea()
	return fitTo(w, h, wantW, wantH)
}

// fitTo 是纯逻辑版本：给定工作区，算出窗口尺寸。
//
// 收敛规则，按优先级：
//  1. 不超过工作区的 90%——留出边距，否则最大化时窗口会贴边、拖不动。
//  2. 工作区够大时维持期望尺寸——用户没要求最大化，就不该替他最大化。
//  3. 工作区处于可用区间内时不低于 1000×640，否则登录页会挤到两栏互相压。
//  4. 工作区本身就很小时绝不撑到下限：那只会让窗口超出屏幕。
func fitTo(workW, workH, wantW, wantH int) (int, int) {
	const (
		maxRatio   = 0.90
		minW, minH = 1000, 640
	)

	w := scale(wantW, workW, maxRatio)
	h := scale(wantH, workH, maxRatio)

	if workW > minW && w < minW {
		w = minW
	}
	if workH > minH && h < minH {
		h = minH
	}
	if w > workW {
		w = workW
	}
	if h > workH {
		h = workH
	}
	// 极端小屏下的最后兜底：低于这个尺寸界面已无法正常操作，但窗口本身
	// 不能是 0 或负数。
	if w < 640 {
		w = 640
	}
	if h < 480 {
		h = 480
	}
	return w, h
}

func scale(v, limit int, ratio float64) int {
	limit = int(float64(limit) * ratio)
	if v > limit {
		return limit
	}
	return v
}

// workArea 返回当前主显示器的工作区尺寸（去掉任务栏）。
func workArea() (int, int) {
	type rect struct{ left, top, right, bottom int32 }
	var r rect
	// SPI_GETWORKAREA 需要一个 RECT 指针。用 unsafe 而不是反射：
	// 每开一次窗口调用一次，不值得为此引入反射开销。
	ret, _, _ := procSystemParametersGet.Call(
		uintptr(spiGetWorkArea),
		0,
		uintptr(unsafe.Pointer(&r)),
		0,
	)
	if ret != 0 && r.right > r.left && r.bottom > r.top {
		return int(r.right - r.left), int(r.bottom - r.top)
	}
	// 取不到工作区就退回整屏，至少还能给出正确的方向。
	w, _, _ := procGetSystemMetrics.Call(uintptr(smCXScreen))
	h, _, _ := procGetSystemMetrics.Call(uintptr(smCYScreen))
	if w == 0 || h == 0 {
		return 1920, 1080
	}
	return int(w), int(h)
}

// showStartupError 用系统对话框报错后退出。
func showStartupError(title, msg string) {
	winmsg.Error(title, msg)
}
