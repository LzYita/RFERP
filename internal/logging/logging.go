package logging

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	maxSize int64 = 5 << 20
	keep          = 3
)

var openLogFile = os.OpenFile

// Options 供服务端覆盖默认的滚动参数。
//
// 为什么要能覆盖：桌面端一天开关几次，5MB×3 足够；服务器是 7×24 常驻，
// 同样的参数下"保留 180 天"根本无从谈起——文件早在第三天就被滚掉了。
// 滚动上限与保留天数必须一起给出，否则日志策略只是句口号。
type Options struct {
	FileName string // 默认 rferp.log
	MaxSize  int64  // 单文件上限；<=0 用默认 5MB
	Keep     int    // 滚动保留份数；<=0 用默认 3
}

func Init(dir string) error {
	return InitWith(dir, Options{})
}

func InitWith(dir string, opt Options) error {
	if dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	name := opt.FileName
	if name == "" {
		name = "rferp.log"
	}
	path := filepath.Join(dir, name)
	size := opt.MaxSize
	if size <= 0 {
		size = maxSize
	}
	n := opt.Keep
	if n <= 0 {
		n = keep
	}
	if err := rotateN(path, size, n); err != nil {
		return err
	}
	writer := &rotatingWriter{path: path, maxSize: size, keep: n}
	if err := writer.open(); err != nil {
		return err
	}
	log.SetOutput(io.MultiWriter(writer, os.Stderr))
	log.SetFlags(log.LstdFlags)
	return nil
}

type rotatingWriter struct {
	mu      sync.Mutex
	file    *os.File
	path    string
	maxSize int64
	keep    int
	size    int64
}

func (w *rotatingWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		if err := w.open(); err != nil {
			return 0, err
		}
	}
	if w.size > 0 && w.size+int64(len(data)) > w.maxSize {
		file := w.file
		w.file = nil
		if err := file.Close(); err != nil {
			return 0, err
		}
		rotateErr := rotateN(w.path, 0, w.keep)
		if err := w.open(); err != nil {
			return 0, errors.Join(rotateErr, err)
		}
		if rotateErr != nil {
			return 0, rotateErr
		}
	}
	n, err := w.file.Write(data)
	w.size += int64(n)
	return n, err
}

func (w *rotatingWriter) open() error {
	file, err := openLogFile(w.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return err
	}
	w.file, w.size = file, info.Size()
	return nil
}

func (w *rotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	file := w.file
	w.file = nil
	return file.Close()
}

func rotateN(path string, size int64, keep int) error {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Size() < size {
		return nil
	}
	if err := os.Remove(fmt.Sprintf("%s.%d", path, keep)); err != nil && !os.IsNotExist(err) {
		return err
	}
	for i := keep - 1; i >= 1; i-- {
		if err := os.Rename(fmt.Sprintf("%s.%d", path, i), fmt.Sprintf("%s.%d", path, i+1)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return os.Rename(path, path+".1")
}

// PruneOlderThan 删除 dir 下修改时间早于 age 的日志文件及其滚动副本。
//
// 只动本包认识的两种命名（rferp.log / rferp.log.N / *.log），
// 不递归、不删子目录：日志目录与备份目录同属"用户数据目录"，误删的代价
// 远大于少留几天日志。
func PruneOlderThan(dir string, age time.Duration) (int, error) {
	if dir == "" || age <= 0 {
		return 0, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	cutoff := time.Now().Add(-age)
	removed := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if name == "rferp.log" {
			continue
		}
		if !strings.HasSuffix(name, ".log") && !strings.Contains(name, ".log.") {
			continue
		}
		info, err := e.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		if err := os.Remove(filepath.Join(dir, name)); err == nil {
			removed++
		}
	}
	return removed, nil
}
