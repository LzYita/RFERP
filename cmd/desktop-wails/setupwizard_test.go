package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestEnsureWritableDirPreservesExistingProbe(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".rferp-write-test")
	const original = "existing user content"
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	if err := ensureWritableDir(dir); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != original {
		t.Fatalf("existing probe changed: %q, %v", data, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != ".rferp-write-test" {
		t.Fatalf("temporary probe leaked: %v, %v", entries, err)
	}
}

func TestEnsureWritableDirCreatesAndCleansDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "new", "data")
	if err := ensureWritableDir(dir); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("probe not cleaned: %v, %v", entries, err)
	}
}

func TestEnsureWritableDirRejectsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(path, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := ensureWritableDir(path); err == nil {
		t.Fatal("regular file accepted as a directory")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "keep" {
		t.Fatalf("file changed: %q, %v", data, err)
	}
}

func TestEnsureWritableDirAcceptsLinkedRoot(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(t.TempDir(), "data")
	if runtime.GOOS == "windows" {
		if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, dir).CombinedOutput(); err != nil {
			t.Fatalf("create junction: %v: %s", err, out)
		}
	} else if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	if err := ensureWritableDir(link); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("probe leaked in linked directory: %v, %v", entries, err)
	}
}

func TestPingMySQLRejectsInvalidIdentifierBeforeConnecting(t *testing.T) {
	for _, name := range []string{"", "app`name", "app;DROP", "app name", "数据库", strings.Repeat("a", 65)} {
		err := pingMySQL(mysqlInput{Host: "invalid.invalid", Port: 3306, DBName: name})
		if err == nil || err.Error() != "数据库名不合法" {
			t.Errorf("name %q reached connection instead of validation: %v", name, err)
		}
	}
}
