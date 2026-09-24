package repository

import "os"

func osMkdirAll(dir string) error {
	return os.MkdirAll(dir, 0o755)
}
