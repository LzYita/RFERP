//go:build !windows

package paths

import "path/filepath"

func filesystemPath(path string) (string, error) { return filepath.EvalSymlinks(path) }
