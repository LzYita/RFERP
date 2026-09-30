package dbbackup

import (
	"os"
	"path/filepath"
	"testing"
)

func TestVerifyDumpFileRejectsTruncatedDump(t *testing.T) {
	dir := t.TempDir()

	truncated := filepath.Join(dir, "truncated.sql")
	if err := os.WriteFile(truncated, []byte("INSERT INTO products VALUES (1);\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := VerifyDumpFile(truncated); err == nil {
		t.Error("a dump without the completion marker must be rejected")
	}

	empty := filepath.Join(dir, "empty.sql")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := VerifyDumpFile(empty); err == nil {
		t.Error("an empty file must be rejected")
	}

	if err := VerifyDumpFile(filepath.Join(dir, "missing.sql")); err == nil {
		t.Error("a missing file must be rejected")
	}
}

func TestVerifyDumpFileAcceptsCompleteDump(t *testing.T) {
	dir := t.TempDir()
	// 标记必须落在尾部区间内；mysqldump 正是这样写的。
	path := filepath.Join(dir, "ok.sql")
	body := "INSERT INTO products VALUES (1);\n-- Dump completed on 2026-01-01 00:00:00\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := VerifyDumpFile(path); err != nil {
		t.Errorf("a complete dump must pass verification: %v", err)
	}
}

func TestPruneSnapshotsKeepsNewestFilesPerPrefix(t *testing.T) {
	dir := t.TempDir()
	// 6 份 pre_restore + 2 份 pre_clear + 1 份常规备份
	for i := 1; i <= 6; i++ {
		name := "pre_restore_2026010" + string(rune('0'+i)) + "_000000.sql"
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for i := 1; i <= 2; i++ {
		name := "pre_clear_2026010" + string(rune('0'+i)) + "_000000.sql"
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "backup_20260101_000000.sql"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	removed := PruneSnapshots(dir, "pre_restore", 5)
	if len(removed) != 1 || removed[0] != "pre_restore_20260101_000000.sql" {
		t.Fatalf("PruneSnapshots removed %v, want the oldest pre_restore file", removed)
	}

	// 其它前缀与常规备份不受影响
	for _, keep := range []string{
		"pre_clear_20260101_000000.sql",
		"pre_clear_20260102_000000.sql",
		"backup_20260101_000000.sql",
		"pre_restore_20260102_000000.sql",
	} {
		if _, err := os.Stat(filepath.Join(dir, keep)); err != nil {
			t.Errorf("%s should have been kept: %v", keep, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "pre_restore_20260101_000000.sql")); !os.IsNotExist(err) {
		t.Error("the oldest pre_restore file should have been removed")
	}
}

func TestPruneSnapshotsIsNoopWhenUnderLimit(t *testing.T) {
	dir := t.TempDir()
	for i := 1; i <= 3; i++ {
		name := "pre_clear_2026010" + string(rune('0'+i)) + "_000000.sql"
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if removed := PruneSnapshots(dir, "pre_clear", 5); len(removed) != 0 {
		t.Fatalf("PruneSnapshots removed %v, want nothing", removed)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("directory has %d files, want 3", len(entries))
	}
}

func TestPruneSnapshotsToleratesMissingDir(t *testing.T) {
	if removed := PruneSnapshots(filepath.Join(t.TempDir(), "nope"), "pre_restore", 5); len(removed) != 0 {
		t.Fatalf("PruneSnapshots on a missing dir removed %v", removed)
	}
}

func TestPruneSnapshotsKeepsSQLiteAndMySQLCopiesSeparately(t *testing.T) {
	dir := t.TempDir()
	for _, ext := range []string{".db", ".sql"} {
		for i := 1; i <= 6; i++ {
			name := "pre_restore_2026010" + string(rune('0'+i)) + "_000000" + ext
			if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	removed := PruneSnapshots(dir, "pre_restore", 5)
	if len(removed) != 2 || removed[0] != "pre_restore_20260101_000000.db" || removed[1] != "pre_restore_20260101_000000.sql" {
		t.Fatalf("removed %v, want oldest SQLite and MySQL copies", removed)
	}
	for _, ext := range []string{".db", ".sql"} {
		if _, err := os.Stat(filepath.Join(dir, "pre_restore_20260106_000000"+ext)); err != nil {
			t.Fatalf("latest %s copy missing: %v", ext, err)
		}
	}
}
