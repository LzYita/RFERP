//go:build !windows

package svcrun

// runAsService 在非 Windows 平台恒为 false：没有 SCM 这回事。
//
// 保留这个函数而不是在 svcrun.go 里写平台判断，是为了让 Windows 实现不必
// 引入 `_ = windows` 之类的空引用，也避免 Linux 的 CI（codeql / MySQL 作业
// 会 `go build ./...`）编译失败。
func runAsService(_ string, _ Main) (bool, error) {
	return false, nil
}
