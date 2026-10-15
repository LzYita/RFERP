package repository

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
)

// 开库重试（2026-10-15 实机演练带出来的修复）。
//
// 事故：软件更新是"先启动新进程、老进程再退出"，新进程只等单实例锁，而单实例锁比
// 文件句柄先释放，于是新进程在老进程还在拆 WAL/SHM 句柄时开库，驱动报
// `disk I/O error (1546)`。用户看到"RFERP 无法启动"，手动再点一次就好了。
// 复现脚本见 docs/plans/wails-migration.md（Round 8 关口记录）。
//
// busy_timeout(5000) 救不了这个错误：它只处理 BUSY/LOCKED，不处理主码 10（IOERR）。
//
// 这里测三件事，且**不依赖真的两个进程抢文件**（那在测试里做不到）：替换掉
// openSQLiteOnce，让它按剧本失败。
type fakeSQLError struct {
	code int
	msg  string
}

func (e *fakeSQLError) Error() string { return fmt.Sprintf("%s (%d)", e.msg, e.code) }
func (e *fakeSQLError) Code() int     { return e.code }

// stubOpenSQLite 让 openSQLiteOnce 按剧本响应，返回记录调用次数的闭包。
// script 里每一项：nil 表示成功，其余是错误。nil 项用尽后返回最后一个错误。
func stubOpenSQLite(t *testing.T, script ...error) *int {
	t.Helper()
	calls := 0
	prev := openSQLiteOnce
	openSQLiteOnce = func(path string) (*sqlx.DB, error) {
		i := calls
		calls++
		if i >= len(script) {
			i = len(script) - 1
		}
		if err := script[i]; err != nil {
			return nil, err
		}
		return openRealSQLite(path)
	}
	t.Cleanup(func() { openSQLiteOnce = prev })
	return &calls
}

// openRealSQLite 打开真实的库（用于让"成功"那一步是真的）。
func openRealSQLite(path string) (*sqlx.DB, error) {
	dsn := "file:" + filepath.ToSlash(path) +
		"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	db, err := sqlx.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func tmpDBPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "rferp.db")
}

func ioErr() error   { return &fakeSQLError{code: 1546, msg: "disk I/O error"} }
func busyErr() error { return &fakeSQLError{code: 5, msg: "database is locked"} }

// 瞬时错误会被重试，并且最终成功——这正是升级后第一次启动要遇到的场景。
func TestOpenSQLiteRetriesTransientErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"IOERR", ioErr()},
		{"BUSY", busyErr()},
		{"LOCKED", &fakeSQLError{code: 6, msg: "database table is locked"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := tmpDBPath(t)
			calls := stubOpenSQLite(t, tc.err, tc.err, nil)

			db, err := OpenSQLite(path)
			if err != nil {
				t.Fatalf("重试后应当成功，却报错: %v", err)
			}
			defer db.Close()
			if *calls != 3 {
				t.Errorf("尝试次数 = %d，期望 3（两次失败 + 一次成功）", *calls)
			}
			// 拿到的必须是真的能用的库，不只是 nil
			var one int
			if err := db.Get(&one, "SELECT 1"); err != nil || one != 1 {
				t.Errorf("重试后返回的库不可用: one=%d err=%v", one, err)
			}
		})
	}
}

// extended code 的低 8 位才是主码：1546 = (10 | 24<<8)，主码 10 = SQLITE_IOERR。
// 只判等值会把 1546 判成"不可重试"，那正好漏掉这次要修的真实错误。
func TestTransientSQLiteErrorUsesPrimaryCode(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"extended IOERR_READ(1546)", ioErr(), true},
		{"主码 IOERR(10)", &fakeSQLError{code: 10, msg: "disk I/O error"}, true},
		{"BUSY(5)", busyErr(), true},
		{"LOCKED(6)", &fakeSQLError{code: 6, msg: "locked"}, true},
		{"CANTOPEN(14)", &fakeSQLError{code: 14, msg: "unable to open database file"}, false},
		{"CORRUPT(11)", &fakeSQLError{code: 11, msg: "database disk image is malformed"}, false},
		{"NOTADB(26)", &fakeSQLError{code: 26, msg: "file is not a database"}, false},
		{"READONLY(8)", &fakeSQLError{code: 8, msg: "attempt to write a readonly database"}, false},
		{"不是驱动错误", errors.New("boom"), false},
		{"包一层的驱动错误", fmt.Errorf("打开本机数据库: %w", ioErr()), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := transientSQLiteError(c.err); got != c.want {
				t.Errorf("transientSQLiteError(%v) = %v，期望 %v", c.err, got, c.want)
			}
		})
	}
}

// 不可重试的错误要**立刻**返回：路径错、文件损坏这类是配置问题，重试只会把一个
// 明确的报错拖成几秒后才出现。
func TestOpenSQLiteDoesNotRetryPermanentErrors(t *testing.T) {
	path := tmpDBPath(t)
	perm := &fakeSQLError{code: 14, msg: "unable to open database file"}
	calls := stubOpenSQLite(t, perm)

	if _, err := OpenSQLite(path); err == nil {
		t.Fatal("期望报错")
	}
	if *calls != 1 {
		t.Errorf("尝试次数 = %d，期望 1（不可重试的错误不应重试）", *calls)
	}
}

// 一直失败时用尽全部次数，并把**最后一次**的错误原样返回（不吞、不替换）。
func TestOpenSQLiteGivesUpAfterAllAttempts(t *testing.T) {
	path := tmpDBPath(t)
	calls := stubOpenSQLite(t, ioErr())

	_, err := OpenSQLite(path)
	if err == nil {
		t.Fatal("期望报错")
	}
	if want := len(sqliteOpenAttempts); *calls != want {
		t.Errorf("尝试次数 = %d，期望 %d（用尽全部退避）", *calls, want)
	}
	var coder interface{ Code() int }
	if !errors.As(err, &coder) || coder.Code() != 1546 {
		t.Errorf("返回的错误 = %v，期望保留最后一次驱动错误 1546", err)
	}
}

// 退避表本身：必须非空、首个为 0（第一次不睡）、其余递增。
func TestSQLiteOpenBackoffShape(t *testing.T) {
	if len(sqliteOpenAttempts) == 0 {
		t.Fatal("退避表不能为空")
	}
	if sqliteOpenAttempts[0] != 0 {
		t.Errorf("第一次尝试前不该等待，实际 %s", sqliteOpenAttempts[0])
	}
	total := time.Duration(0)
	for i, d := range sqliteOpenAttempts[1:] {
		if d < 200*time.Millisecond {
			t.Errorf("第 %d 次退避 %s，短于下限 200ms（杀软/索引器可能慢，抖一下就重试没有意义）", i+2, d)
		}
		total += d
	}
	if total > 10*time.Second {
		t.Errorf("总退避 %s，超过 10 秒就该让用户手动重试了", total)
	}
}

// 兜底：不注入时也能真的开一个库（防止 seam 抽坏以后没人发现）。
func TestOpenSQLiteStillWorksForReal(t *testing.T) {
	path := tmpDBPath(t)
	db, err := OpenSQLite(path)
	if err != nil {
		t.Fatalf("真实开库失败: %v", err)
	}
	defer db.Close()
	if _, err := os.Stat(path); err != nil {
		t.Errorf("库文件没落盘: %v", err)
	}
}
