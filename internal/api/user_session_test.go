package api

import (
	"errors"
	"net/http"
	"path/filepath"
	"testing"

	"app/internal/auth"
	"app/internal/model"
	"app/internal/usecase"
)

type userMutationApps struct {
	sessionApps
	err error
}

func (a *userMutationApps) UpdateUser(in usecase.UpdateUserInput) error { return a.err }
func (a *userMutationApps) DeleteUser(id int64) error                   { return a.err }

func TestUserMutationRevokesCredentials(t *testing.T) {
	for _, mutation := range []string{"disable", "disable-zero", "delete", "role", "edit"} {
		for _, failed := range []bool{false, true} {
			name := mutation + "/success"
			if failed {
				name = mutation + "/failed"
			}
			t.Run(name, func(t *testing.T) {
				u := user(7, "viewer", auth.RoleViewer)
				apps := &userMutationApps{sessionApps: sessionApps{users: []model.User{*u}}}
				if failed {
					apps.err = errors.New("mutation refused")
				}
				s := New(apps, WithRemember(filepath.Join(t.TempDir(), "remember.bin")))
				tok := issue(t, s, u)
				admin := issueAdmin(t, s)
				rt, err := s.remember.issue(u)
				if err != nil {
					t.Fatal(err)
				}
				if err := s.remember.saveToDisk(u, rt); err != nil {
					t.Fatal(err)
				}
				method, path := "PUT", "/api/users"
				body := map[string]any{"id": u.ID, "role": u.Role, "status": 1, "displayName": "updated"}
				if mutation == "disable" {
					body["status"] = 2
				}
				if mutation == "disable-zero" {
					body["status"] = 0
				}
				if mutation == "role" {
					body["role"] = "warehouse"
				}
				if mutation == "delete" {
					method, path = "DELETE", "/api/users?id=7"
				}
				want := http.StatusOK
				if failed {
					want = http.StatusBadRequest
				}
				if w := postJSON(t, s, method, path, admin, body); w.Code != want {
					t.Fatalf("mutation = %d, want %d: %s", w.Code, want, w.Body.String())
				}
				revoked := !failed && mutation != "edit"
				want = http.StatusOK
				if revoked {
					want = http.StatusUnauthorized
				}
				if w := getWithToken(t, s, "GET", "/api/me", tok); w.Code != want {
					t.Errorf("old session = %d, want %d", w.Code, want)
				}
				if _, ok := s.remember.exchange(rt); ok == revoked {
					t.Errorf("remember credential valid = %v, revoked = %v", ok, revoked)
				}
				if _, ok := s.remember.loadFromDisk(); ok == revoked {
					t.Errorf("disk credential exists = %v, revoked = %v", ok, revoked)
				}
				if w := getWithToken(t, s, "GET", "/api/me", admin); w.Code != http.StatusOK {
					t.Errorf("other user = %d", w.Code)
				}
			})
		}
	}
}
