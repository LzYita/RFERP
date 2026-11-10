//go:build !windows

package nativefiledialog

// PickFile 在非 Windows 平台没有原生的"打开文件"对话框可用。
//
// 桌面入口只在 Windows 上构建；这里返回 false 让调用方走"没有文件可选"
// 的分支，而不是 panic 或静默返回一个不存在的路径。
func PickFile(title, defExt string) (string, bool) {
	return "", false
}
