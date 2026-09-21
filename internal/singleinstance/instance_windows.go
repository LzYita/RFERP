//go:build windows

package singleinstance

import (
	"syscall"
	"time"
	"unsafe"
)

var (
	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	procCreateMutexW = kernel32.NewProc("CreateMutexW")
)

const errorAlreadyExists = 183

var mutexHandle uintptr

// Acquire tries to take the named single-instance mutex. If it is already held,
// it retries for up to `wait` (useful right after an update, when the previous
// process is still exiting). Returns false when still held after the wait.
func Acquire(name string, wait time.Duration) (bool, error) {
	p, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return false, err
	}
	deadline := time.Now().Add(wait)
	for {
		h, _, callErr := procCreateMutexW.Call(0, 0, uintptr(unsafe.Pointer(p)))
		if h == 0 {
			return false, callErr
		}
		if errno, ok := callErr.(syscall.Errno); ok && errno == errorAlreadyExists {
			syscall.CloseHandle(syscall.Handle(h))
			if time.Now().After(deadline) {
				return false, nil
			}
			time.Sleep(500 * time.Millisecond)
			continue
		}
		mutexHandle = h
		return true, nil
	}
}
