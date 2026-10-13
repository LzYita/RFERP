package paths

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestResolveUnder(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "backup.sql")
	if err := os.WriteFile(inside, []byte("backup"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{root, inside} {
		got, err := ResolveUnder(root, target)
		want, evalErr := filepath.EvalSymlinks(target)
		if err != nil || evalErr != nil || got != want {
			t.Fatalf("ResolveUnder(%q) = %q, %v; want %q", target, got, err, want)
		}
	}
	for _, target := range []string{"", t.TempDir(), filepath.Join(root, "missing.sql")} {
		if got, err := ResolveUnder(root, target); err == nil || got != "" {
			t.Errorf("invalid target %q = %q, %v", target, got, err)
		}
	}
	if got, err := ResolveUnder("", inside); err == nil || got != "" {
		t.Fatalf("empty root = %q, %v", got, err)
	}
}

func directoryLink(t *testing.T, link, target string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
			t.Fatalf("create junction: %v: %s", err, out)
		}
	} else if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

func TestResolveUnderLongPath(t *testing.T) {
	root := t.TempDir()
	target := root
	for i := 0; i < 5; i++ {
		target = filepath.Join(target, strings.Repeat("a", 60))
	}
	if err := os.MkdirAll(target, 0700); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveUnder(root, target)
	want, evalErr := filepath.EvalSymlinks(target)
	if err != nil || evalErr != nil || got != want {
		t.Fatalf("long path = %q, %v; want %q", got, err, want)
	}
}

func TestResolveUnderDirectoryLinks(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "real")
	if err := os.Mkdir(inside, 0700); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	directoryLink(t, filepath.Join(root, "escape"), outside)
	if got, err := ResolveUnder(root, filepath.Join(root, "escape")); err == nil || got != "" {
		t.Fatalf("outside link accepted: %q, %v", got, err)
	}
	directoryLink(t, filepath.Join(root, "safe"), inside)
	got, err := ResolveUnder(root, filepath.Join(root, "safe"))
	want, evalErr := filepath.EvalSymlinks(inside)
	if err != nil || evalErr != nil || got != want {
		t.Fatalf("inside link = %q, %v; want %q", got, err, want)
	}
	// A configured root may itself be a link; its real children remain usable.
	rootLink := filepath.Join(t.TempDir(), "root")
	directoryLink(t, rootLink, root)
	got, err = ResolveUnder(rootLink, filepath.Join(rootLink, "real"))
	if err != nil || got != want {
		t.Fatalf("linked root = %q, %v; want %q", got, err, want)
	}
}

func TestResolveUnderFileSymlinks(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.sql")
	if err := os.WriteFile(outside, []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "backup.sql")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("file symlinks unavailable: %v", err)
	}
	if got, err := ResolveUnder(root, link); err == nil || got != "" {
		t.Fatalf("outside file link accepted: %q, %v", got, err)
	}
}
