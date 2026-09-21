//go:build !windows

package singleinstance

import "time"

func Acquire(name string, wait time.Duration) (bool, error) {
	return true, nil
}
