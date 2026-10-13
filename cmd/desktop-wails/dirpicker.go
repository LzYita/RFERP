package main

import (
	"context"
	"sync"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// 原生目录选择框（Wails 形态）。
//
// 为什么绕这一圈，而不是让前端直接调 Wails 绑定：web/ 是**宿主无关**的——
// 同一份页面要能跑在 vite dev、纯浏览器（自动化关口）与内嵌服务里，全程不引用
// window.go / wailsjs。把原生能力从宿主注入、由服务端裁决，前端只发一个普通
// HTTP 请求，架构边界与 WithHostOperations 一致。
//
// 时序问题：内嵌服务在 wails.Run **之前**就建好了，而 runtime.OpenDirectoryDialog
// 需要 wails.Run 才产生的 context.Context。所以这里不是把 ctx 捕获进闭包，
// 而是留一个可后填的槽位——ctx 在 OnStartup 写入，picker 在那之后才可能被调用。
type nativeDirPicker struct {
	mu  sync.RWMutex
	ctx context.Context
}

// newNativeDirPicker 返回一个目录选择器（可直接注入 api.Server）。
// 注入时 ctx 还是空的，这没问题：真正调用发生在窗口起来之后。
// 返回具体类型是为了让调用方能用 setCtx 在 OnStartup 里回填 ctx。
func newNativeDirPicker() *nativeDirPicker {
	return &nativeDirPicker{}
}

// setCtx 由 wails.Run 的 OnStartup 调用。
func (p *nativeDirPicker) setCtx(ctx context.Context) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ctx = ctx
}

// PickDirectory 实现 api.DirectoryPicker。
func (p *nativeDirPicker) PickDirectory(current string) (string, error) {
	p.mu.RLock()
	ctx := p.ctx
	p.mu.RUnlock()
	if ctx == nil {
		// 窗口还没起来（或这是 -serve 无头模式）。回错误而不是空串：
		// 空串在接口里表示"用户取消"，两者对界面的含义完全不同。
		return "", errPickerNotReady
	}
	return runtime.OpenDirectoryDialog(ctx, runtime.OpenDialogOptions{
		Title:            "请选择数据目录",
		DefaultDirectory: current,
	})
}

var errPickerNotReady = &pickerError{"窗口尚未就绪，无法弹出目录选择框"}

type pickerError struct{ msg string }

func (e *pickerError) Error() string { return e.msg }
