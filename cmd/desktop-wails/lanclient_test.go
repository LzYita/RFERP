package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"app/internal/api"
	"app/internal/config"
)

func TestSwitchServerDoesNotForwardPreviousCredential(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	authorization := make(chan string, 2)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization <- r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"appVersion":"2.0.0","schemaVersion":20}`))
	}))
	defer target.Close()
	client := api.NewClient("http://previous.invalid:8080")
	client.SetToken("previous-session")
	cfg := &config.Config{Mode: config.ModeClient, ServerURL: client.ServerURL(), DataDir: filepath.Join(t.TempDir(), "data")}
	got, err := switchServer(client, cfg, target.URL)
	if err != nil || got != target.URL || client.ServerURL() != target.URL || client.Token() != "" {
		t.Fatalf("switch=%q target=%q tokenCleared=%v err=%v", got, client.ServerURL(), client.Token() == "", err)
	}
	if _, err := client.Describe(); err != nil {
		t.Fatal(err)
	}
	if len(authorization) != 2 || <-authorization != "" || <-authorization != "" {
		t.Fatal("previous credential was sent to the new host")
	}
}

func TestFailedSwitchPreservesPreviousHost(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer target.Close()
	client := api.NewClient("http://previous.invalid:8080")
	client.SetToken("previous-session")
	cfg := &config.Config{Mode: config.ModeClient, ServerURL: client.ServerURL()}
	if _, err := switchServer(client, cfg, target.URL); err == nil {
		t.Fatal("failed probe was accepted")
	}
	if client.ServerURL() != "http://previous.invalid:8080" || client.Token() != "previous-session" || cfg.ServerURL != client.ServerURL() {
		t.Fatal("failed probe changed the previous host or credential")
	}
}

func TestNormalizeConfiguredServerURL(t *testing.T) {
	for _, example := range []struct{ input, want string }{
		{"192.168.1.10", "http://192.168.1.10:8080"},
		{"192.168.1.10:18080", "http://192.168.1.10:18080"},
		{"https://example.test:8443/", "https://example.test:8443"},
	} {
		t.Run(example.input, func(t *testing.T) {
			got, err := normalizeServerURL(example.input)
			if err != nil || got != example.want {
				t.Fatalf("normalize=%q want=%q err=%v", got, example.want, err)
			}
		})
	}
}

func TestFailedSwitchSavePreservesPreviousHost(t *testing.T) {
	blocked := filepath.Join(t.TempDir(), "blocked")
	if err := os.WriteFile(blocked, []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("APPDATA", blocked)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"appVersion":"2.0.0"}`))
	}))
	defer target.Close()
	client := api.NewClient("http://previous.invalid:8080")
	client.SetToken("previous-session")
	cfg := &config.Config{Mode: config.ModeClient, ServerURL: client.ServerURL()}
	if _, err := switchServer(client, cfg, target.URL); err == nil {
		t.Fatal("failed configuration save was accepted")
	}
	if client.ServerURL() != "http://previous.invalid:8080" || client.Token() != "previous-session" || cfg.ServerURL != client.ServerURL() {
		t.Fatal("failed save changed the previous host or credential")
	}
}
