package paths

import (
	"strings"

	"golang.org/x/sys/windows"
)

// EvalSymlinks does not resolve every Windows directory junction.
func filesystemPath(path string) (string, error) {
	if !strings.HasPrefix(path, `\\?\`) {
		if strings.HasPrefix(path, `\\`) {
			path = `\\?\UNC\` + strings.TrimPrefix(path, `\\`)
		} else {
			path = `\\?\` + path
		}
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	h, err := windows.CreateFile(name, 0,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, 256)
	for {
		n, err := windows.GetFinalPathNameByHandle(h, &buf[0], uint32(len(buf)), 0)
		if err != nil {
			return "", err
		}
		if n < uint32(len(buf)) {
			path := windows.UTF16ToString(buf[:n])
			if strings.HasPrefix(path, `\\?\UNC\`) {
				return `\\` + strings.TrimPrefix(path, `\\?\UNC\`), nil
			}
			return strings.TrimPrefix(path, `\\?\`), nil
		}
		buf = make([]uint16, n+1)
	}
}
