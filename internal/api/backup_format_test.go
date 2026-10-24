package api

import (
	"app/internal/paths"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestBackupFormatUsesContent(t *testing.T) {
	s := newBackupServer(t, &backupApps{}, WithHostOperations())
	tok := issueAdmin(t, s)
	if err := os.MkdirAll(paths.BackupDir(), 0755); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, content, format string }{
		{"backup_20260101_000001.sql", "SQLite format 3\x00payload", "sqlite"},
		{"backup_20260101_000002.db", "-- Dump completed\n", "mysql"},
	} {
		if err := os.WriteFile(filepath.Join(paths.BackupDir(), tc.name), []byte(tc.content), 0600); err != nil {
			t.Fatal(err)
		}
		w := doJSON(t, s, "POST", "/api/restore", tok, map[string]string{"file": tc.name})
		var got struct {
			Format string `json:"format"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if w.Code != 200 || got.Format != tc.format {
			t.Fatalf("restore format=%d %s", w.Code, w.Body.String())
		}
	}
	w := doJSON(t, s, "GET", "/api/backup/files", tok, nil)
	var list struct {
		Files []backupEntry `json:"files"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Files) != 2 {
		t.Fatalf("files=%d", len(list.Files))
	}
	for _, f := range list.Files {
		want := "mysql"
		if filepath.Ext(f.Name) == ".sql" {
			want = "sqlite"
		}
		if f.Format != want {
			t.Errorf("%s format=%s want %s", f.Name, f.Format, want)
		}
	}
}
