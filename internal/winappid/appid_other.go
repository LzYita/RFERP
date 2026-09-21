//go:build !windows

package winappid

// Set is a no-op on non-Windows platforms.
func Set(appID string) {}
