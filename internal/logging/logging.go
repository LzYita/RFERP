package logging

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
)

const (
	maxSize int64 = 5 << 20
	keep          = 3
)

func Init(dir string) error {
	if dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	path := filepath.Join(dir, "rferp.log")
	rotate(path)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	log.SetOutput(io.MultiWriter(f, os.Stderr))
	log.SetFlags(log.LstdFlags)
	return nil
}

func rotate(path string) {
	info, err := os.Stat(path)
	if err != nil || info.Size() < maxSize {
		return
	}
	os.Remove(fmt.Sprintf("%s.%d", path, keep))
	for i := keep - 1; i >= 1; i-- {
		os.Rename(fmt.Sprintf("%s.%d", path, i), fmt.Sprintf("%s.%d", path, i+1))
	}
	os.Rename(path, path+".1")
}
