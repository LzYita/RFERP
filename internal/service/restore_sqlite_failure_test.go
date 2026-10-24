package service

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"app/internal/repository"
	"app/internal/usecase"
)

func TestRestoreSQLiteReportsUnknownWhenFileReplacementFails(t *testing.T) {
	t.Setenv("MES_DATA_DIR", t.TempDir())
	cause := errors.New("file replacement interrupted")
	port := &fakeSnapshotPort{kind: "sqlite", restoreErr: cause}
	svc := NewWithSnapshot(nil, port, nil)
	path := filepath.Join(t.TempDir(), "backup_20260101_000000.db")
	seedRestoreSQLite(t, path)
	res, err := svc.RestoreDatabase(path)
	requireOperationState(t, err, usecase.OperationUnknown, cause)
	if res.PreRestore == "" {
		t.Fatal("lost safety copy path on failed replacement")
	}
}

func seedRestoreSQLite(t *testing.T, path string) {
	t.Helper()
	db, err := repository.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec("CREATE TABLE fixture (id INTEGER PRIMARY KEY)")
	closeErr := db.Close()
	if err != nil {
		t.Fatal(err)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
}

func TestRestoreSQLiteRejectsCorruptFileBeforeClosing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "corrupt.db")
	if err := os.WriteFile(path, []byte("SQLite format 3\x00broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	port := &fakeSnapshotPort{kind: "sqlite"}
	svc := NewWithSnapshot(nil, port, nil)
	res, err := svc.RestoreDatabase(path)
	if err == nil || res.RequiresRestart || port.restored != "" || len(port.prefixes) != 0 {
		t.Fatalf("corrupt input reached restore: result=%+v err=%v port=%+v", res, err, port)
	}
}
