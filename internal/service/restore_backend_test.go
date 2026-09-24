package service_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"app/internal/dbfile"
	"app/internal/service"
)

// 备份类型按文件内容判断，不看扩展名；UI 与 Service 必须同一口径。
func TestIsSQLiteSnapshotUsesFileHeaderNotExtension(t *testing.T) {
	dir := t.TempDir()

	// SQLite 魔数 + 伪装成 .sql 的文件名
	snapshot := filepath.Join(dir, "backup.sql")
	body := append([]byte("SQLite format 3\x00"), []byte("rest of the file")...)
	if err := os.WriteFile(snapshot, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if !dbfile.IsSQLiteSnapshot(snapshot) {
		t.Fatal("SQLite magic with a .sql name must still be detected as a snapshot")
	}

	// SQL dump 伪装成 .db
	dump := filepath.Join(dir, "backup.db")
	if err := os.WriteFile(dump, []byte("INSERT INTO products VALUES (1);\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if dbfile.IsSQLiteSnapshot(dump) {
		t.Fatal("a SQL dump named .db must not be treated as a snapshot")
	}

	if dbfile.IsSQLiteSnapshot(filepath.Join(dir, "missing.db")) {
		t.Fatal("a missing file must not be treated as a snapshot")
	}
}

func TestRestoreDatabaseRejectsCrossBackendBackup(t *testing.T) {
	dir := t.TempDir()
	snapshot := filepath.Join(dir, "s.db")
	if err := os.WriteFile(snapshot, []byte("SQLite format 3\x00padding"), 0o600); err != nil {
		t.Fatal(err)
	}
	dump := filepath.Join(dir, "d.sql")
	if err := os.WriteFile(dump, []byte("INSERT INTO products VALUES (1);\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// MySQL 存储：拒绝 SQLite 整库快照
	mysqlSvc := service.New(nil, "", "", nil)
	if _, _, err := mysqlSvc.RestoreDatabase(snapshot); err == nil || !strings.Contains(err.Error(), "MySQL") {
		t.Fatalf("mysql backend accepted a SQLite snapshot: err=%v", err)
	}

	// SQLite 存储：拒绝 MySQL 的 .sql 备份
	sqliteSvc := service.NewWithSnapshot(nil, service.NewSQLiteSnapshotPort(filepath.Join(dir, "live.db"), nil), nil)
	if _, _, err := sqliteSvc.RestoreDatabase(dump); err == nil || !strings.Contains(err.Error(), "SQLite") {
		t.Fatalf("sqlite backend accepted a .sql dump: err=%v", err)
	}
}
