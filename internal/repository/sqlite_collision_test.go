package repository

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

var collisionTime = time.Date(2026, 10, 14, 12, 34, 56, 0, time.UTC)

func collisionDatabase(t *testing.T, path string, value int) {
	t.Helper()
	db, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("CREATE TABLE sentinel (value INTEGER)"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO sentinel VALUES (?)", value); err != nil {
		t.Fatal(err)
	}
}

func collisionValue(t *testing.T, path string, want int) {
	t.Helper()
	db, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var got int
	if err := db.Get(&got, "SELECT value FROM sentinel"); err != nil || got != want {
		t.Fatalf("database %s: value=%d, error=%v, want %d", path, got, err, want)
	}
}

func collisionBytes(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestSnapshotSQLiteFileToPreservesExistingOutput(t *testing.T) {
	for _, empty := range []bool{false, true} {
		t.Run(fmt.Sprintf("empty=%v", empty), func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "source.db")
			collisionDatabase(t, src, 9)
			out := filepath.Join(dir, "backup_20261014_123456.db")
			if empty {
				if err := os.WriteFile(out, nil, 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				collisionDatabase(t, out, 7)
			}
			before := collisionBytes(t, out)
			path, err := snapshotSQLiteFileTo(src, dir, "", collisionTime)
			if !errors.Is(err, os.ErrExist) || path != "" {
				t.Fatalf("path=%q, error=%v, want existing-target rejection", path, err)
			}
			if !bytes.Equal(before, collisionBytes(t, out)) {
				t.Fatal("existing output changed")
			}
			collisionValue(t, src, 9)
		})
	}
}

func TestSnapshotSQLiteFileToSameTimePreservesFirstSnapshot(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "source.db")
	collisionDatabase(t, src, 9)
	out, err := snapshotSQLiteFileTo(src, dir, "pre_clear", collisionTime)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(out) != "pre_clear_20261014_123456.db" {
		t.Fatalf("unexpected name: %s", out)
	}
	before := collisionBytes(t, out)
	if _, err := snapshotSQLiteFileTo(src, dir, "pre_clear", collisionTime); !errors.Is(err, os.ErrExist) {
		t.Fatalf("second snapshot: %v", err)
	}
	if !bytes.Equal(before, collisionBytes(t, out)) {
		t.Fatal("first snapshot changed")
	}
	collisionValue(t, out, 9)
	collisionValue(t, src, 9)
}

func TestSnapshotSQLiteFileToConcurrentCallsPreserveOneSnapshot(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "source.db")
	collisionDatabase(t, src, 9)
	errorsByCall := make(chan error, 8)
	start := make(chan struct{})
	for i := 0; i < cap(errorsByCall); i++ {
		go func() {
			<-start
			_, err := snapshotSQLiteFileTo(src, dir, "backup", collisionTime)
			errorsByCall <- err
		}()
	}
	close(start)
	winners := 0
	for i := 0; i < cap(errorsByCall); i++ {
		if err := <-errorsByCall; err == nil {
			winners++
		} else if !errors.Is(err, os.ErrExist) {
			t.Errorf("unexpected concurrent error: %v", err)
		}
	}
	if winners != 1 {
		t.Fatalf("successful writers=%d, want 1", winners)
	}
	collisionValue(t, filepath.Join(dir, "backup_20261014_123456.db"), 9)
	collisionValue(t, src, 9)
}

func TestSnapshotSQLiteFileToCleansFailedOutput(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "invalid.db")
	if err := os.WriteFile(src, []byte("not a database"), 0600); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(dir, "backup_20261013_123456.db")
	if err := os.WriteFile(old, []byte("old backup"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := snapshotSQLiteFileTo(src, dir, "backup", collisionTime); err == nil {
		t.Fatal("expected source error")
	}
	if _, err := os.Stat(filepath.Join(dir, "backup_20261014_123456.db")); !os.IsNotExist(err) {
		t.Fatalf("failed output remains: %v", err)
	}
	if string(collisionBytes(t, old)) != "old backup" {
		t.Fatal("older backup changed")
	}
	if string(collisionBytes(t, src)) != "not a database" {
		t.Fatal("source changed")
	}
}

func TestRestoreSQLiteFilePreservesExistingSidecar(t *testing.T) {
	for _, empty := range []bool{false, true} {
		t.Run(fmt.Sprintf("empty=%v", empty), func(t *testing.T) {
			dir := t.TempDir()
			live, snapshot := filepath.Join(dir, "live.db"), filepath.Join(dir, "snapshot.db")
			collisionDatabase(t, live, 9)
			collisionDatabase(t, snapshot, 7)
			pre := live + ".before-restore-20261014_123456"
			if empty {
				if err := os.WriteFile(pre, nil, 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				collisionDatabase(t, pre, 3)
			}
			before := collisionBytes(t, pre)
			_, err := restoreSQLiteFile(live, snapshot, collisionTime)
			if !errors.Is(err, os.ErrExist) {
				t.Fatalf("expected collision, got %v", err)
			}
			if !bytes.Equal(before, collisionBytes(t, pre)) {
				t.Fatal("pre-existing recovery backup changed")
			}
			collisionValue(t, live, 9)
			collisionValue(t, snapshot, 7)
			if _, err := os.Stat(live + ".restore-tmp"); !os.IsNotExist(err) {
				t.Fatalf("unexpected staging file: %v", err)
			}
		})
	}
}

func TestRestoreSQLiteFilePreservesExistingStage(t *testing.T) {
	dir := t.TempDir()
	live, snapshot := filepath.Join(dir, "live.db"), filepath.Join(dir, "snapshot.db")
	collisionDatabase(t, live, 9)
	collisionDatabase(t, snapshot, 7)
	stage := live + ".restore-tmp"
	if err := os.WriteFile(stage, []byte("existing stage"), 0600); err != nil {
		t.Fatal(err)
	}
	pre, err := restoreSQLiteFile(live, snapshot, collisionTime)
	if !errors.Is(err, os.ErrExist) {
		t.Fatalf("expected stage collision, got %v", err)
	}
	if string(collisionBytes(t, stage)) != "existing stage" {
		t.Fatal("existing stage changed")
	}
	collisionValue(t, live, 9)
	collisionValue(t, snapshot, 7)
	collisionValue(t, pre, 9)
}

func TestRestoreSQLiteFileSameTimePreservesFirstBackup(t *testing.T) {
	dir := t.TempDir()
	live, snapshot, next := filepath.Join(dir, "live.db"), filepath.Join(dir, "snapshot.db"), filepath.Join(dir, "next.db")
	collisionDatabase(t, live, 9)
	collisionDatabase(t, snapshot, 7)
	collisionDatabase(t, next, 3)
	pre, err := restoreSQLiteFile(live, snapshot, collisionTime)
	if err != nil {
		t.Fatal(err)
	}
	before := collisionBytes(t, pre)
	if _, err := restoreSQLiteFile(live, next, collisionTime); !errors.Is(err, os.ErrExist) {
		t.Fatalf("second restore: %v", err)
	}
	if !bytes.Equal(before, collisionBytes(t, pre)) {
		t.Fatal("first recovery backup changed")
	}
	collisionValue(t, live, 7)
	collisionValue(t, pre, 9)
}

func TestCopyFileCleansOnlyOwnedFailedCopy(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "incomplete")
	if err := copyFile(t.TempDir(), out, os.O_EXCL); err == nil {
		t.Fatal("directory source must fail")
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatalf("incomplete owned copy remains: %v", err)
	}
	src := filepath.Join(dir, "source")
	if err := os.WriteFile(src, []byte("replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(out, []byte("existing"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := copyFile(src, out, os.O_EXCL); !errors.Is(err, os.ErrExist) {
		t.Fatalf("existing copy: %v", err)
	}
	if string(collisionBytes(t, out)) != "existing" {
		t.Fatal("existing copy changed")
	}
	if err := copyFile(src, out, os.O_TRUNC); err != nil {
		t.Fatal(err)
	}
	if string(collisionBytes(t, out)) != "replacement" {
		t.Fatal("intentional rollback copy did not replace target")
	}
}
