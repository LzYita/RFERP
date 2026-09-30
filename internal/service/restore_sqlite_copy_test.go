package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"app/internal/migrate"
	"app/internal/model"
	"app/internal/repository"
)

func TestRestoreSQLiteSafetyCopyIsAUsableSnapshot(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("MES_DATA_DIR", dataDir)
	path := filepath.Join(dataDir, "live.db")
	db, err := repository.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrate.RunSQLite(db, migrate.SQLiteOptions{}); err != nil {
		t.Fatal(err)
	}
	store := repository.NewSQLite(db)
	if _, err := store.CreateUser(&model.User{Username: "before", PasswordHash: "test-hash", Role: "viewer", Status: 1}); err != nil {
		t.Fatal(err)
	}
	backup, err := repository.SnapshotSQLiteFile(path, filepath.Join(dataDir, "备份"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateUser(&model.User{Username: "after", PasswordHash: "test-hash", Role: "viewer", Status: 1}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	svc := NewWithSnapshot(nil, NewSQLiteSnapshotPort(path, db.Close), nil)

	res, err := svc.RestoreDatabase(backup)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(filepath.Base(res.PreRestore), "pre_restore_") || filepath.Ext(res.PreRestore) != ".db" {
		t.Fatalf("safety copy is not selectable: %s", res.PreRestore)
	}
	if _, err := os.Stat(res.PreRestore); err != nil {
		t.Fatalf("missing safety copy: %v", err)
	}
	if err := repository.VerifySQLiteFile(res.PreRestore); err != nil {
		t.Fatalf("invalid safety copy: %v", err)
	}
	for _, tc := range []struct {
		path string
		want int
	}{
		{path, 1}, {res.PreRestore, 2},
	} {
		check, err := repository.OpenSQLite(tc.path)
		if err != nil {
			t.Fatal(err)
		}
		var count int
		err = check.Get(&count, "SELECT COUNT(*) FROM users")
		_ = check.Close()
		if err != nil || count != tc.want {
			t.Fatalf("users in %s = %d, err %v, want %d", tc.path, count, err, tc.want)
		}
	}
}
