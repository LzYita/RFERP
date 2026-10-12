//go:build !windows

package windowsobj

// Guard is a no-op off Windows; there is no job object to join.
func Guard() error { return nil }

// Guarded is always false off Windows.
func Guarded() bool { return false }
