package service

import (
	"os"
	"path/filepath"
	"testing"

	"app/internal/paths"
)

func TestSetDataDirCreatesOperatorSelectedDirectory(t *testing.T) {
	before := paths.DataDir()
	t.Cleanup(func() { paths.SetDataDir(before) })
	target := filepath.Join(t.TempDir(), "chosen", "data")
	if err := (&Service{}).SetDataDir(target); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(target)
	if err != nil || !info.IsDir() || paths.DataDir() != target {
		t.Fatalf("selected directory not applied: path=%q stat=%v", paths.DataDir(), err)
	}
}

func TestSetDataDirRejectsFileWithoutChangingRoot(t *testing.T) {
	before := paths.DataDir()
	t.Cleanup(func() { paths.SetDataDir(before) })
	target := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(target, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := (&Service{}).SetDataDir(target); err == nil {
		t.Fatal("file accepted as directory")
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "keep" || paths.DataDir() != before {
		t.Fatalf("failed selection changed file/root: data=%q root=%q err=%v", data, paths.DataDir(), err)
	}
}
