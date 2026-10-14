//go:build !windows

// Package noconsole is a no-op off Windows. There is no console-allocation
// rule to work around: a child either inherits the parent's terminal or,
// under a display server, never had one.
package noconsole

import "os/exec"

// Hide does nothing off Windows.
func Hide(cmd *exec.Cmd) {}
