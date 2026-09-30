package service

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"app/internal/usecase"
)

func TestRestoreSQLiteReportsUnknownWhenFileReplacementFails(t *testing.T) {
	t.Setenv("MES_DATA_DIR", t.TempDir())
	cause := errors.New("file replacement interrupted")
	port := &fakeSnapshotPort{kind: "sqlite", restoreErr: cause}
	svc := NewWithSnapshot(nil, port, nil)
	path := filepath.Join(t.TempDir(), "backup_20260101_000000.db")
	if err := os.WriteFile(path, []byte("SQLite format 3\x00"), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := svc.RestoreDatabase(path)
	requireOperationState(t, err, usecase.OperationUnknown, cause)
	if res.PreRestore == "" {
		t.Fatal("lost safety copy path on failed replacement")
	}
}
