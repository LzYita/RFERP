//go:build !windows

package singleinstance

func Acquire(name string) (bool, error) {
	return true, nil
}
