package logging

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRunningLogRotation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rferp.log")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	writer := &rotatingWriter{file: file, path: path, maxSize: 8, keep: 2}
	for _, message := range []string{"first\n", "second\n", "third\n", "fourth\n"} {
		if n, err := writer.Write([]byte(message)); err != nil || n != len(message) {
			t.Fatalf("write=%d err=%v", n, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	for suffix, want := range map[string]string{"": "fourth\n", ".1": "third\n", ".2": "second\n"} {
		got, err := os.ReadFile(path + suffix)
		if err != nil || string(got) != want {
			t.Fatalf("%s=%q want=%q err=%v", suffix, got, want, err)
		}
	}
	if _, err := os.Stat(path + ".3"); !os.IsNotExist(err) {
		t.Fatalf("rotation exceeded retained file count: %v", err)
	}
}

func TestPruneKeepsActiveLogAndUserFiles(t *testing.T) {
	dir := t.TempDir()
	old := time.Now().Add(-48 * time.Hour)
	for _, name := range []string{"rferp.log", "rferp.log.1", "notes.txt", "recent.log"} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("retained"), 0600); err != nil {
			t.Fatal(err)
		}
		if name != "recent.log" {
			if err := os.Chtimes(path, old, old); err != nil {
				t.Fatal(err)
			}
		}
	}
	if removed, err := PruneOlderThan(dir, 24*time.Hour); err != nil || removed != 1 {
		t.Fatalf("removed=%d err=%v", removed, err)
	}
	for _, name := range []string{"rferp.log", "notes.txt", "recent.log"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("%s removed: %v", name, err)
		}
	}
}
