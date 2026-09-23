package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBootstrapAdminOnlyWhenEmpty(t *testing.T) {
	apps := &fakeApps{userCount: 0}
	s := New(apps)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/bootstrap-admin", "application/json",
		bytes.NewReader([]byte(`{"username":"boss","password":"pass1234","display_name":"Boss"}`)))
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("bootstrap status = %d", resp.StatusCode)
	}

	apps.userCount = 1
	resp2, err := http.Post(ts.URL+"/api/bootstrap-admin", "application/json",
		bytes.NewReader([]byte(`{"username":"x","password":"pass1234"}`)))
	if err != nil {
		t.Fatalf("bootstrap2: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusConflict {
		t.Fatalf("second bootstrap status = %d, want 409", resp2.StatusCode)
	}
}
