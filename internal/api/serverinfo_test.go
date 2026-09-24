package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"app/internal/usecase"
)

type fakeDescriptor struct {
	info usecase.ServerInfo
	err  error
}

func (f fakeDescriptor) Describe() (usecase.ServerInfo, error) { return f.info, f.err }

func TestWriteServerInfoReportsVersionAndDatabase(t *testing.T) {
	rec := httptest.NewRecorder()
	writeServerInfo(rec, fakeDescriptor{info: usecase.ServerInfo{
		APIVersion: 1, SchemaVersion: 13, DatabaseID: "db-1", Storage: "mysql",
	}}, "1.3.0")

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200", rec.Code)
	}
	var got usecase.ServerInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.AppVersion != "1.3.0" || got.DatabaseID != "db-1" ||
		got.SchemaVersion != 13 || got.APIVersion != 1 || got.Storage != "mysql" {
		t.Fatalf("unexpected payload: %+v", got)
	}
}

// 服务器自描述必须免登录可取：预检发生在建立会话之前。
func TestServerInfoNeedsNoAuth(t *testing.T) {
	s := &Server{
		desc:     fakeDescriptor{info: usecase.ServerInfo{APIVersion: 1, SchemaVersion: 13, DatabaseID: "db-2", Storage: "mysql"}},
		version:  "2.0.0",
		sessions: make(map[string]*session),
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/serverinfo")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%s, want 200 without a token", resp.Status)
	}
	var got usecase.ServerInfo
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.DatabaseID != "db-2" || got.AppVersion != "2.0.0" {
		t.Fatalf("payload=%+v", got)
	}
}

func TestProbeReadsServerInfo(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/serverinfo" {
			t.Errorf("path=%s", r.URL.Path)
		}
		writeServerInfo(w, fakeDescriptor{info: usecase.ServerInfo{
			APIVersion: 1, SchemaVersion: 13, DatabaseID: "db-7", Storage: "mysql",
		}}, "9.9.9")
	}))
	defer ts.Close()

	// 带尾部斜杠也要能用（与 NewClient 的容错一致）。
	info, err := Probe(ts.URL + "/")
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if info.DatabaseID != "db-7" || info.AppVersion != "9.9.9" || info.APIVersion != 1 {
		t.Fatalf("info=%+v", info)
	}
}

func TestProbeRejectsEmptyAndUnreachable(t *testing.T) {
	if _, err := Probe("   "); err == nil {
		t.Fatal("expected an error for an empty address")
	}
	if _, err := Probe("http://127.0.0.1:1"); err == nil {
		t.Fatal("expected an error for an unreachable server")
	}
}
