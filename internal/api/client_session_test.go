package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"app/internal/model"
)

func TestClientModeDisablesLocalRememberedLogin(t *testing.T) {
	apps := &clientApps{fakeApps: &fakeApps{loginUser: testWarehouseUser(), userCount: 1}}
	server := New(apps, WithRemember(filepath.Join(t.TempDir(), "remember.bin")))
	login := doJSONWithHeaders(t, server, "POST", "/api/login",
		map[string]any{"username": "wh", "password": "test", "remember": true}, nil)
	var result map[string]any
	if err := json.Unmarshal(login.Body.Bytes(), &result); err != nil || login.Code != 200 {
		t.Fatalf("login=%d err=%v", login.Code, err)
	}
	if _, issued := result["remember_token"]; issued {
		t.Fatal("client issued an unusable local remembered credential")
	}
	load := doJSONWithHeaders(t, server, "GET", "/api/remember/load", nil, nil)
	var capability struct {
		Token   *string `json:"token"`
		Enabled bool    `json:"enabled"`
	}
	if err := json.Unmarshal(load.Body.Bytes(), &capability); err != nil || load.Code != 200 || capability.Enabled || capability.Token != nil {
		t.Fatalf("remember capability=%+v code=%d err=%v", capability, load.Code, err)
	}
	if got := doJSONWithHeaders(t, server, "POST", "/api/remember/exchange", map[string]string{"token": "old"}, nil).Code; got != 404 {
		t.Fatalf("client remember exchange=%d want=404", got)
	}
}

func TestSwitchClearsLocalAuthentication(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[fail], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "remember.bin")
			server := New(lanApps(), WithRemember(path), WithLANConnect(func(raw string) (string, error) {
				if fail {
					return "", errors.New("probe failed")
				}
				return raw, nil
			}))
			user := testWarehouseUser()
			token, err := server.issueSession(user)
			if err != nil {
				t.Fatal(err)
			}
			rememberToken, err := server.remember.issue(user)
			if err != nil {
				t.Fatal(err)
			}
			if err := server.remember.saveToDisk(user, rememberToken); err != nil {
				t.Fatal(err)
			}
			response := doJSONWithHeaders(t, server, "POST", "/api/lan/connect",
				map[string]string{"url": "http://new.invalid"}, nil)
			me := doJSONWithHeaders(t, server, "GET", "/api/me", nil,
				map[string]string{"Authorization": "Bearer " + token})
			if fail {
				if response.Code != 400 || me.Code != 200 || server.remember.activeCount() != 1 {
					t.Fatal("failed switch changed authentication")
				}
			} else {
				if response.Code != 200 || me.Code != 401 || server.remember.activeCount() != 0 {
					t.Fatal("successful switch kept authentication")
				}
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("remembered credential remains: %v", err)
				}
			}
		})
	}
}

func TestClientAuthenticationFailuresReachLocalAPI(t *testing.T) {
	for _, status := range []int{401, 403, 426} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var remoteStatus atomic.Int32
			remoteStatus.Store(200)
			host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/login":
					writeJSON(w, 200, map[string]any{
						"token": "host-session", "user": model.User{ID: 7, Username: "admin", Role: "admin"},
					})
				case "/api/me":
					if got := int(remoteStatus.Load()); got != 200 {
						writeCodeErr(w, got, "host_policy", "host rejected session")
					} else {
						writeJSON(w, 200, model.User{ID: 7, Username: "admin", Role: "admin"})
					}
				}
			}))
			defer host.Close()
			server := New(NewClient(host.URL))
			login := doJSONWithHeaders(t, server, "POST", "/api/login",
				map[string]string{"username": "admin", "password": "test"}, nil)
			var result struct {
				Token string `json:"token"`
			}
			if err := json.Unmarshal(login.Body.Bytes(), &result); err != nil || login.Code != 200 {
				t.Fatalf("login=%d err=%v", login.Code, err)
			}
			remoteStatus.Store(int32(status))
			response := doJSONWithHeaders(t, server, "GET", "/api/parts", nil,
				map[string]string{"Authorization": "Bearer " + result.Token})
			var problem struct {
				Code string `json:"code"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil || response.Code != status || problem.Code != "host_policy" {
				t.Fatalf("response=%d code=%q err=%v", response.Code, problem.Code, err)
			}
			remoteStatus.Store(200)
			me := doJSONWithHeaders(t, server, "GET", "/api/me", nil,
				map[string]string{"Authorization": "Bearer " + result.Token})
			want := 200
			if status == 401 {
				want = 401
			}
			if me.Code != want {
				t.Fatalf("local session=%d want=%d", me.Code, want)
			}
		})
	}
}

func TestBusinessHandlersPreserveUpstreamErrors(t *testing.T) {
	for _, method := range []string{"GET", "POST"} {
		for _, status := range []int{401, 403, 426} {
			t.Run(method+"/"+http.StatusText(status), func(t *testing.T) {
				host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/api/me" {
						writeJSON(w, 200, model.User{ID: 7, Role: "admin"})
					} else {
						writeCodeErr(w, status, "business_policy", "operation rejected")
					}
				}))
				defer host.Close()
				client := NewClient(host.URL)
				client.SetToken("host-session")
				server := New(client)
				token, err := server.issueSession(&model.User{ID: 7, Role: "admin"})
				if err != nil {
					t.Fatal(err)
				}
				var body any
				if method == "POST" {
					body = map[string]string{"code": "P1", "name": "part"}
				}
				response := doJSONWithHeaders(t, server, method, "/api/parts", body,
					map[string]string{"Authorization": "Bearer " + token})
				var problem struct {
					Code string `json:"code"`
				}
				if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil || response.Code != status || problem.Code != "business_policy" {
					t.Fatalf("response=%d code=%q err=%v", response.Code, problem.Code, err)
				}
			})
		}
	}
}

func TestClientErrorsKeepCodesAcrossResponseFormats(t *testing.T) {
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeCodeErr(w, http.StatusForbidden, "license_expired", "read only")
	}))
	defer host.Close()
	client := NewClient(host.URL)
	for name, operation := range map[string]func() error{
		"json":            func() error { _, err := client.ListParts(); return err },
		"bytes":           func() error { _, err := client.doBytes("POST", "/api/export/audit/download", nil); return err },
		"create-backup":   func() error { _, err := client.CreateBackupStream(); return err },
		"download-backup": func() error { _, err := client.StreamBackup("backup.db"); return err },
	} {
		t.Run(name, func(t *testing.T) {
			var upstream *APIError
			err := operation()
			if !errors.As(err, &upstream) || upstream.Status != 403 || upstream.Code != "license_expired" || upstream.Message != "read only" {
				t.Fatalf("upstream error lost its contract: %v", err)
			}
		})
	}
}

func TestHostSwitchRejectsInFlightLogin(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		writeJSON(w, 200, map[string]any{"token": "old-host-session", "user": model.User{ID: 7}})
	}))
	defer host.Close()
	client := NewClient(host.URL)
	result := make(chan error, 1)
	go func() {
		_, err := client.Login("admin", "test")
		result <- err
	}()
	<-entered
	client.SetServerURL("http://new.invalid:8080")
	client.SetServerURL(host.URL)
	close(release)
	var upstream *APIError
	if err := <-result; !errors.As(err, &upstream) || upstream.Status != 401 {
		t.Fatalf("old-host login was accepted after a switch: %v", err)
	}
	if client.Token() != "" {
		t.Fatal("old-host login restored a credential after a switch")
	}
}
