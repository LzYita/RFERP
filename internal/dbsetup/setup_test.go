package dbsetup

import (
	"strings"
	"testing"
)

func TestValidIdentifier(t *testing.T) {
	for _, name := range []string{"a", "app_2026", "0123", strings.Repeat("A", 64)} {
		if !ValidIdentifier(name) {
			t.Errorf("valid identifier rejected: %q", name)
		}
	}
	for _, name := range []string{"", strings.Repeat("a", 65), "app`name", "app;DROP", "app name", "a\n", "a-b", "数据库"} {
		if ValidIdentifier(name) {
			t.Errorf("unsafe identifier accepted: %q", name)
		}
	}
}

func TestSetupRejectsInvalidIdentifiersBeforeConnecting(t *testing.T) {
	if err := EnsureDatabase("not a DSN", "app`name"); err == nil || err.Error() != "数据库名不合法" {
		t.Fatalf("EnsureDatabase did not validate first: %v", err)
	}
	for _, args := range [][2]string{{"app`user", "app"}, {"app", "app`db"}} {
		if err := EnsureUser("not a DSN", args[0], "unused", args[1]); err == nil || err.Error() != "用户名或数据库名不合法" {
			t.Errorf("EnsureUser(%q, %q) did not validate first: %v", args[0], args[1], err)
		}
	}
}
