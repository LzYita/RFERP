//go:build windows

// Package noconsole launches console programs from a GUI program without
// flashing a console window.
//
// Why this exists. The desktop build is linked with -H windowsgui, so the host
// itself owns no console. Windows gives a console-subsystem child a *fresh*
// console when the parent has none, and that fresh console is a visible window.
// Measured on this machine: clicking "restore backup" and "clear database" each
// popped a black window, because both operations take a safety copy first
// (service.safetyCopy -> dbbackup.BackupTo) and that shells out to mysqldump.exe.
// No error is reported and the operation still succeeds; it is purely the window.
//
// CREATE_NO_WINDOW is the right flag, and HideWindow is not. Both suppress
// display, but HideWindow only sets SW_HIDE on an already-allocated console --
// the console is still created and can still flash before it is hidden.
// CREATE_NO_WINDOW tells the kernel not to allocate one at all. It is also
// inherited by grandchildren, which covers the case where a child shells out
// again.
package noconsole

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// Hide prepares cmd so that starting it does not create a console window.
// It must be called before cmd.Start or cmd.Run.
//
// Existing SysProcAttr are preserved: a caller that already set other
// attributes (or windows only, for instance) keeps them, and this only adds a
// flag. Calling Hide twice is harmless.
func Hide(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	attr := cmd.SysProcAttr
	if attr == nil {
		attr = &syscall.SysProcAttr{}
		cmd.SysProcAttr = attr
	}
	attr.CreationFlags |= windows.CREATE_NO_WINDOW
}
