package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"app/internal/usecase"
)

func TestRestoreRestartGate(t *testing.T) {
	for _, tc := range []struct {
		name            string
		restart, failed bool
	}{
		{"sqlite", true, false}, {"uncertain replacement", true, true}, {"mysql", false, false}, {"rejected input", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			exited := make(chan struct{}, 1)
			apps := &backupApps{restoreFn: func(string) (usecase.RestoreResult, error) {
				r := usecase.RestoreResult{RequiresRestart: tc.restart}
				if tc.failed {
					return r, errors.New("restore failed")
				}
				return r, nil
			}}
			s := newBackupServer(t, apps, WithHostOperations(), WithRemember(filepath.Join(t.TempDir(), "remember.bin")), WithProcessExit(func() { exited <- struct{}{} }))
			tok := issueAdmin(t, s)
			u := user(1, "admin", "admin")
			rt, err := s.remember.issue(u)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.remember.saveToDisk(u, rt); err != nil {
				t.Fatal(err)
			}
			if w := doJSON(t, s, "POST", "/api/restore/exit", tok, nil); w.Code != http.StatusConflict {
				t.Fatalf("exit before restore = %d", w.Code)
			}
			seedBackupFile(t, "backup_20260925_101530.sql")
			w := doJSON(t, s, "POST", "/api/restore", tok, map[string]string{"file": "backup_20260925_101530.sql"})
			if !tc.failed {
				var result usecase.RestoreResult
				if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				if w.Code != 200 || result.RequiresRestart != tc.restart {
					t.Fatalf("restore = %d %s", w.Code, w.Body.String())
				}
			}
			want := http.StatusOK
			if _, ok := s.remember.loadFromDisk(); ok == tc.restart {
				t.Errorf("remember disk remains=%v, restart=%v", ok, tc.restart)
			}
			if tc.restart {
				want = http.StatusServiceUnavailable
			}
			if got := getWithToken(t, s, "GET", "/api/me", tok).Code; got != want {
				t.Fatalf("post restore request = %d want %d", got, want)
			}
			if !tc.restart {
				return
			}
			if got := getWithToken(t, s, "GET", "/api/users/count", "").Code; got != 503 {
				t.Fatalf("bootstrap bypass = %d", got)
			}
			if got := doJSON(t, s, "POST", "/api/restore/exit", "", nil).Code; got != 401 {
				t.Fatalf("anonymous exit = %d", got)
			}
			if got := doJSON(t, s, "POST", "/api/restore/exit", tok, nil).Code; got != 200 {
				t.Fatalf("authorized exit = %d", got)
			}
			select {
			case <-exited:
			case <-time.After(3 * time.Second):
				t.Fatal("exit not scheduled")
			}
		})
	}
}

func TestRestoreExitRequiresHost(t *testing.T) {
	s := newBackupServer(t, &backupApps{})
	if got := doJSON(t, s, "POST", "/api/restore/exit", issueAdmin(t, s), nil).Code; got != http.StatusForbidden {
		t.Fatalf("remote exit=%d", got)
	}
}
