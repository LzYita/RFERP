//go:build windows

// Package windowsobj pins the process tree to a Windows job object so that no
// child survives the parent, however the parent dies.
package windowsobj

import (
	"fmt"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

// job 故意常驻。句柄一旦关闭，KILL_ON_JOB_CLOSE 立刻生效——包括我们自己。
// 句柄由系统在进程退出时回收，那正是我们想要的时机。
var job windows.Handle

// Guard puts the current process, and everything it spawns afterwards, into a
// job marked JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE.
//
// Why this exists. WebView2 runs as a separate msedgewebview2.exe tree. Measured
// on this machine: a clean close and even a Stop-Process -Force both let that
// tree exit on its own, so orphans were not the normal case. What the job object
// removes is the *unattended* case -- a crash, a hard power loss, or a kill that
// happens between WebView2 spawning a child and Chromium's own teardown running.
// Then the kernel tears down the tree when the last handle goes away, without the
// host cooperating.
//
// It is deliberately cheap insurance, not a claimed fix. A regression test
// asserted that orphans occur without it; that assertion was wrong, and the
// finding is recorded here rather than quietly dropped.
//
// Call it before anything that can spawn a child. Failures are returned rather
// than fatal: a missing guarantee is worth reporting, but not worth refusing to
// start over.
func Guard() error {
	if runtime.GOOS != "windows" {
		return nil
	}
	if job != 0 {
		return nil // already guarded
	}

	h, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return fmt.Errorf("CreateJobObject: %w", err)
	}

	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	if _, err := windows.SetInformationJobObject(
		h,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	); err != nil {
		windows.CloseHandle(h)
		return fmt.Errorf("SetInformationJobObject: %w", err)
	}

	if err := windows.AssignProcessToJobObject(h, windows.CurrentProcess()); err != nil {
		windows.CloseHandle(h)
		// 失败通常意味着已经身处某个不允许嵌套的 Job 里（例如某些启动器）。
		// 这时退回到"没有保险但仍可运行"，而不是退出。
		return fmt.Errorf("AssignProcessToJobObject: %w", err)
	}

	job = h
	return nil
}

// Guarded reports whether Guard succeeded.
func Guarded() bool { return job != 0 }
