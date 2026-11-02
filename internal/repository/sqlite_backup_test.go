package repository

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"app/internal/migrate"
	"app/internal/model"
)

func TestSQLiteVacuumIntoSnapshotVerified(t *testing.T) {
	path := filepath.Join(testTempDir(t), "src.db")
	db, err := OpenSQLite(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	if _, err := migrate.RunSQLite(db, migrate.SQLiteOptions{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store := NewSQLite(db)
	if _, err := store.CreateUser(&model.User{Username: "u", PasswordHash: "h", Role: "viewer", Status: 1}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	saveDir := filepath.Join(testTempDir(t), "backups")
	out, err := SnapshotSQLiteFile(path, saveDir)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	sdb, err := OpenSQLite(out)
	if err != nil {
		t.Fatalf("open snapshot: %v", err)
	}
	defer sdb.Close()
	var n int
	if err := sdb.Get(&n, `SELECT COUNT(*) FROM users`); err != nil || n != 1 {
		t.Fatalf("snapshot users=%d err=%v", n, err)
	}
}

func TestTranslateErrorUnique(t *testing.T) {
	err := TranslateError(errors.New("UNIQUE constraint failed: products.code"))
	if !errors.Is(err, ErrUniqueConflict) {
		t.Fatalf("err=%v", err)
	}
	err = TranslateError(errors.New("Error 1062: Duplicate entry 'x' for key 'code'"))
	if !errors.Is(err, ErrUniqueConflict) {
		t.Fatalf("err=%v", err)
	}
}

func TestRestoreSQLiteFileRollsBackToSnapshot(t *testing.T) {
	dir := testTempDir(t)
	path := filepath.Join(dir, "src.db")
	db, err := OpenSQLite(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := migrate.RunSQLite(db, migrate.SQLiteOptions{}); err != nil {
		db.Close()
		t.Fatalf("migrate: %v", err)
	}
	store := NewSQLite(db)
	if _, err := store.CreateUser(&model.User{Username: "p1", PasswordHash: "h", Role: "viewer", Status: 1}); err != nil {
		db.Close()
		t.Fatalf("seed p1: %v", err)
	}
	saveDir := filepath.Join(dir, "backups")
	p1, err := SnapshotSQLiteFile(path, saveDir)
	if err != nil {
		db.Close()
		t.Fatalf("snapshot p1: %v", err)
	}
	if _, err := store.CreateUser(&model.User{Username: "p2", PasswordHash: "h", Role: "viewer", Status: 1}); err != nil {
		db.Close()
		t.Fatalf("seed p2: %v", err)
	}
	// Must close before replace (Windows file lock).
	db.Close()

	pre, err := RestoreSQLiteFile(path, p1)
	if err != nil {
		t.Fatalf("restore p1: %v", err)
	}
	if pre == "" {
		t.Fatal("expected pre-restore backup path")
	}

	rdb, err := OpenSQLite(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer rdb.Close()
	rstore := NewSQLite(rdb)
	users, err := rstore.ListUsers()
	if err != nil {
		t.Fatalf("list users: %v", err)
	}
	if len(users) != 1 || users[0].Username != "p1" {
		t.Fatalf("users=%v, want only p1 (D-014 no merge)", users)
	}
}

// 陈旧 WAL/SHM 若残留，SQLite 会用旧日志回放刚替换进来的文件并造成损坏。
func TestRestoreSQLiteFileRemovesStaleSidecars(t *testing.T) {
	dir := testTempDir(t)
	path := filepath.Join(dir, "src.db")
	db, err := OpenSQLite(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := migrate.RunSQLite(db, migrate.SQLiteOptions{}); err != nil {
		db.Close()
		t.Fatalf("migrate: %v", err)
	}
	store := NewSQLite(db)
	if _, err := store.CreateUser(&model.User{Username: "p1", PasswordHash: "h", Role: "viewer", Status: 1}); err != nil {
		db.Close()
		t.Fatalf("seed: %v", err)
	}
	snapshot, err := SnapshotSQLiteFile(path, filepath.Join(dir, "backups"))
	if err != nil {
		db.Close()
		t.Fatalf("snapshot: %v", err)
	}
	db.Close()

	// 模拟上次非正常退出留下的 WAL/SHM
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := os.WriteFile(path+suffix, []byte("stale"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := RestoreSQLiteFile(path, snapshot); err != nil {
		t.Fatalf("restore: %v", err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(path + suffix); !os.IsNotExist(err) {
			t.Fatalf("stale %s survived restore", suffix)
		}
	}
}

// 同一秒内连续两次备份必须都成功：文件名用 .2 后缀避让，时间戳不变。
//
// 现场场景：用户连点两次「一键备份」，或自动备份与手动备份落在同一秒。
// 修复前第二次会以 "The file exists" 失败，界面只看到"备份失败"。
func TestSnapshotSQLiteFileAllowsSameSecondRepeat(t *testing.T) {
	dir := testTempDir(t)
	src := filepath.Join(dir, "src.db")
	db, err := OpenSQLite(src)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE t (id INTEGER PRIMARY KEY, v TEXT)`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()

	saveDir := filepath.Join(dir, "backups")
	first, err := SnapshotSQLiteFileTo(src, saveDir, "backup")
	if err != nil {
		t.Fatalf("第一次备份: %v", err)
	}
	second, err := SnapshotSQLiteFileTo(src, saveDir, "backup")
	if err != nil {
		t.Fatalf("同一秒内第二次备份应成功: %v", err)
	}
	if first == second {
		t.Fatalf("两次备份落到了同一个文件: %s", first)
	}
	for _, p := range []string{first, second} {
		if err := VerifySQLiteFile(p); err != nil {
			t.Fatalf("备份不可用 %s: %v", p, err)
		}
	}
}
