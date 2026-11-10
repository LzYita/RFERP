package dbbackup

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 升级前备份的参数必须带 --single-transaction：
// 首启向导建的专用账号没有 LOCK TABLES，默认模式会在第一步就被拒。
func TestDumpArgsUseSingleTransaction(t *testing.T) {
	args := dumpArgs("C:\\Temp\\rferp.cnf", "appliancedb")
	joined := strings.Join(args, " ")

	if !strings.Contains(joined, "--single-transaction") {
		t.Fatalf("dumpArgs = %v，缺少 --single-transaction", args)
	}
	if args[0] != "--defaults-extra-file=C:\\Temp\\rferp.cnf" {
		t.Errorf("凭据文件必须是第一个参数: %v", args)
	}
	if args[len(args)-1] != "appliancedb" {
		t.Errorf("库名必须在最后: %v", args)
	}
}

// 找不到工具时必须是可识别的类型错误：入口据此弹「选择 mysqldump.exe」，
// 而不是只把 exec 的英文错误念给用户。
func TestResolveDumpPathMissingIsTyped(t *testing.T) {
	dir := t.TempDir()
	// 隔离 PATH，避免开发机上真装着的 mysqldump 让用例失去意义。
	t.Setenv("PATH", dir)
	missing := filepath.Join(dir, "nowhere", "mysqldump.exe")

	_, err := resolveDumpPath(missing)
	if !errors.Is(err, ErrDumpToolNotFound) {
		t.Fatalf("err = %v，期望 ErrDumpToolNotFound", err)
	}
	if !strings.Contains(err.Error(), "mysqldumpPath") {
		t.Errorf("错误没有告诉用户怎么修: %v", err)
	}
}

// mysqldump 的 stderr 必须带进错误：GUI 形态下 os.Stderr 没人看得到，
// 而权限不足/磁盘满这些真实原因全在那里。
func TestBackupToCarriesDumpStderr(t *testing.T) {
	dir := testTempDir(t)
	t.Setenv(backupHelperModeEnv, "fail-stderr")

	_, err := backupTo("tcp(127.0.0.1:1)/sample", os.Args[0], dir, "backup",
		time.Date(2026, 11, 10, 12, 0, 0, 0, time.Local))
	if err == nil {
		t.Fatal("mysqldump 失败却没报错")
	}
	if !strings.Contains(err.Error(), "Access denied") {
		t.Fatalf("错误里没有 mysqldump 的 stderr，排查时又只能看到 exit status: %v", err)
	}
}
