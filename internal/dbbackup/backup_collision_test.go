package dbbackup

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const backupHelperModeEnv = "RFERP_BACKUP_HELPER_MODE"
const backupHelperMarkerEnv = "RFERP_BACKUP_HELPER_MARKER"

func TestMain(m *testing.M) {
	if mode := os.Getenv(backupHelperModeEnv); mode != "" {
		marker := os.Getenv(backupHelperMarkerEnv)
		if marker != "" {
			_ = os.WriteFile(marker, []byte("called"), 0o600)
		}
		switch mode {
		case "complete":
			_, _ = io.WriteString(os.Stdout, "CREATE TABLE sample (id int);\n-- Dump completed\n")
			os.Exit(0)
		case "fail":
			_, _ = io.WriteString(os.Stdout, "partial dump\n")
			os.Exit(1)
		case "truncated":
			_, _ = io.WriteString(os.Stdout, "partial dump without completion marker\n")
			os.Exit(0)
		default:
			os.Exit(2)
		}
	}
	os.Exit(m.Run())
}

func TestBackupToPreservesExistingOutput(t *testing.T) {
	at := time.Date(2026, 10, 14, 12, 34, 56, 0, time.Local)
	for _, tc := range []struct {
		name, prefix, old string
	}{
		{name: "default prefix, empty existing file", old: ""},
		{name: "manual backup prefix, nonempty existing file", prefix: "backup", old: "keep this dump"},
		{name: "pre_clear prefix, nonempty existing file", prefix: "pre_clear", old: "keep pre-clear dump"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			marker := filepath.Join(dir, "helper-called")
			name := tc.prefix
			if name == "" {
				name = "backup"
			}
			path := filepath.Join(dir, name+"_"+at.Format("20060102_150405")+".sql")
			if err := os.WriteFile(path, []byte(tc.old), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv(backupHelperMarkerEnv, marker)
			t.Setenv(backupHelperModeEnv, "complete")

			got, err := backupTo("tcp(127.0.0.1:1)/sample", os.Args[0], dir, tc.prefix, at)
			if !errors.Is(err, os.ErrExist) {
				t.Fatalf("backupTo error = %v, want os.ErrExist", err)
			}
			if got != "" {
				t.Errorf("backupTo path = %q, want empty on collision", got)
			}
			gotContents, readErr := os.ReadFile(path)
			if readErr != nil {
				t.Fatalf("read existing backup: %v", readErr)
			}
			if string(gotContents) != tc.old {
				t.Errorf("existing backup changed to %q, want %q", gotContents, tc.old)
			}
			if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
				t.Errorf("mysqldump helper was invoked on collision (stat error %v)", statErr)
			}
		})
	}
}

func TestBackupToCreatesCompleteDump(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 10, 14, 12, 34, 56, 0, time.Local)
	marker := filepath.Join(dir, "helper-called")
	t.Setenv(backupHelperModeEnv, "complete")
	t.Setenv(backupHelperMarkerEnv, marker)

	path, err := backupTo("tcp(127.0.0.1:1)/sample", os.Args[0], dir, "backup", at)
	if err != nil {
		t.Fatalf("backupTo: %v", err)
	}
	want := filepath.Join(dir, "backup_"+at.Format("20060102_150405")+".sql")
	if path != want {
		t.Fatalf("backupTo path = %q, want %q", path, want)
	}
	body, err := os.ReadFile(path)
	if err != nil || string(body) != "CREATE TABLE sample (id int);\n-- Dump completed\n" {
		t.Fatalf("dump contents=%q, error=%v", body, err)
	}
	if err := VerifyDumpFile(path); err != nil {
		t.Errorf("VerifyDumpFile(%q): %v", path, err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("mysqldump helper was not invoked: %v", err)
	}
}

func TestBackupToConcurrentCallsPreserveOneCompleteDump(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(backupHelperModeEnv, "complete")
	at := time.Date(2026, 10, 14, 12, 34, 56, 0, time.UTC)
	errorsByCall := make(chan error, 8)
	start := make(chan struct{})
	for i := 0; i < cap(errorsByCall); i++ {
		go func() {
			<-start
			_, err := backupTo("tcp(127.0.0.1:1)/sample", os.Args[0], dir, "backup", at)
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
	body, err := os.ReadFile(filepath.Join(dir, "backup_20261014_123456.sql"))
	if err != nil || string(body) != "CREATE TABLE sample (id int);\n-- Dump completed\n" {
		t.Fatalf("winning dump changed: contents=%q, error=%v", body, err)
	}
}

func TestBackupToCleansOnlyFailedOutput(t *testing.T) {
	at := time.Date(2026, 10, 14, 12, 34, 56, 0, time.Local)
	for _, mode := range []string{"fail", "truncated"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			marker := filepath.Join(dir, "helper-called")
			sibling := filepath.Join(dir, "backup_20261014_123455.sql")
			if err := os.WriteFile(sibling, []byte("preserve sibling"), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv(backupHelperModeEnv, mode)
			t.Setenv(backupHelperMarkerEnv, marker)

			path, err := backupTo("tcp(127.0.0.1:1)/sample", os.Args[0], dir, "backup", at)
			if err == nil {
				t.Fatal("backupTo succeeded for incomplete dump")
			}
			if path != "" {
				t.Errorf("backupTo path = %q, want empty on failure", path)
			}
			if _, statErr := os.Stat(filepath.Join(dir, "backup_"+at.Format("20060102_150405")+".sql")); !os.IsNotExist(statErr) {
				t.Errorf("failed output remains (stat error %v)", statErr)
			}
			got, readErr := os.ReadFile(sibling)
			if readErr != nil || string(got) != "preserve sibling" {
				t.Errorf("sibling changed: contents %q, error %v", got, readErr)
			}
			if _, statErr := os.Stat(marker); statErr != nil {
				t.Errorf("mysqldump helper was not invoked: %v", statErr)
			}
		})
	}
}
