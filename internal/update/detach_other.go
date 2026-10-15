//go:build !windows

package update

import "syscall"

// detachedAttrs 在非 Windows 上没有作业对象可脱离，返回零值即可。
func detachedAttrs() *syscall.SysProcAttr { return nil }
