package api_test

import (
	"os"
	"testing"
	"time"
)

// testTempDir 是 t.TempDir 的可重试替代。
//
// 为什么需要：这些用例会在目录里开 SQLite。Windows 上连接刚 Close 时，
// 驱动/杀毒软件可能还会短暂占用 -wal、-shm，而 testing.TempDir 的清理
// 只做一次 os.RemoveAll——失败就把一个**断言全过**的用例判成
// "TempDir RemoveAll cleanup: The directory is not empty"。
// 这类红灯只在整仓并行、机器较忙时出现，排查成本极高。
//
// 这里改成最多重试 2 秒；仍失败只记录路径、不掩盖用例本身的断言结果。
func testTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "rferp-test-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	t.Cleanup(func() {
		for i := 0; i < 20; i++ {
			if err := os.RemoveAll(dir); err == nil {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Logf("临时目录仍未能删除（可能被安全软件占用）: %s", dir)
	})
	return dir
}
