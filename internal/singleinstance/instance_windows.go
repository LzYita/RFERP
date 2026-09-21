//go:build windows

package singleinstance

import (
	"syscall"
	"unsafe"
)

var (
	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	procCreateMutexW = kernel32.NewProc("CreateMutexW")
)

const errorAlreadyExists = 183

var mutexHandle uintptr

func Acquire(name string) (bool, error) {
	p, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return false, err
	}
	h, _, callErr := procCreateMutexW.Call(0, 0, uintptr(unsafe.Pointer(p)))
	if h == 0 {
		return false, callErr
	}
	if errno, ok := callErr.(syscall.Errno); ok && errno == errorAlreadyExists {
		return false, nil
	}
	mutexHandle = h
	return true, nil
}
