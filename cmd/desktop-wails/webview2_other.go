//go:build !windows

package main

// 非 Windows 平台没有 WebView2，也没有窗口可开，预检直接放行：
// 这些构建只用于 `go build ./...` 与无头测试，不会真的起窗口。
func ensureWebView2() error { return nil }
