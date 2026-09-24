package repository

import (
	"errors"
	"path/filepath"
	"testing"

	"app/internal/migrate"
	"app/internal/model"
)

func TestSQLiteVacuumIntoSnapshotVerified(t *testing.T) {
	path := filepath.Join(t.TempDir(), "src.db")
	db, err := OpenSQLite(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	if _, err := migrate.RunSQLite(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store := NewSQLite(db)
	if _, err := store.CreateUser(&model.User{Username: "u", PasswordHash: "h", Role: "viewer", Status: 1}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	saveDir := filepath.Join(t.TempDir(), "backups")
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
