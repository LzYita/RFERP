//go:build !windows

package nativefiledialog

// PickFolder is a no-op on non-Windows platforms.
func PickFolder(title string) (string, bool) {
	return "", false
}
