package main

import (
	"database/sql"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"app/internal/api"
	"app/internal/config"
	"app/internal/dbsetup"
	"github.com/go-sql-driver/mysql"
)

func TestSetupGateRejectsUnauthenticatedDirectorySelection(t *testing.T) {
	const token = "test-startup-token"
	for _, cookie := range []string{"", "wrong", token} {
		t.Run(cookie, func(t *testing.T) {
			target := filepath.Join(t.TempDir(), "selected")
			body, err := json.Marshal(map[string]string{"dataDir": target, "storage": "sqlite"})
			if err != nil {
				t.Fatal(err)
			}
			want := http.StatusForbidden
			if cookie == token {
				body = []byte("invalid json")
				want = http.StatusBadRequest
			}
			applied := false
			handler := api.StartupGate(token, newSetupHandler(&config.Config{}, func() { applied = true }))
			req := httptest.NewRequest("POST", "/api/setup/apply", strings.NewReader(string(body)))
			if cookie != "" {
				req.AddCookie(&http.Cookie{Name: api.StartupCookieName, Value: cookie})
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, req)
			if response.Code != want {
				t.Fatalf("status=%d want=%d", response.Code, want)
			}
			if applied {
				t.Fatal("invalid request applied configuration")
			}
			if _, err := os.Stat(target); !os.IsNotExist(err) {
				t.Fatalf("invalid request created directory: %v", err)
			}
		})
	}
}

func TestPingMySQLCreatesExactDatabaseName(t *testing.T) {
	dsn, dataDir := os.Getenv("RFERP_DBSETUP_TEST_DSN"), os.Getenv("RFERP_DBSETUP_TEST_DATADIR")
	if dsn == "" || dataDir == "" {
		t.Skip("requires an explicitly isolated MySQL instance")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	host, portText, err := net.SplitHostPort(cfg.Addr)
	port, portErr := strconv.Atoi(portText)
	if err != nil || portErr != nil || cfg.Net != "tcp" || host != "127.0.0.1" || port == 3306 || cfg.DBName != "" {
		t.Fatal("test DSN must select an isolated loopback port without a database")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var actualDir string
	if err := db.QueryRow("SELECT @@datadir").Scan(&actualDir); err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(filepath.Clean(actualDir), filepath.Clean(dataDir)) {
		t.Fatal("server data directory does not match the explicitly isolated directory")
	}
	prefix := "wizard_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	for _, suffix := range []string{"-中文", "`库", "`; select 1; --"} {
		t.Run(suffix, func(t *testing.T) {
			name := prefix + suffix
			if err := pingMySQL(mysqlInput{Host: host, Port: port, User: cfg.User, Password: cfg.Passwd, DBName: name}); err != nil {
				t.Fatal(err)
			}
			quoted, err := dbsetup.QuoteDatabaseName(name)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if _, err := db.Exec("DROP DATABASE IF EXISTS " + quoted); err != nil {
					t.Error(err)
				}
			})
			var actual string
			if err := db.QueryRow("SELECT SCHEMA_NAME FROM information_schema.SCHEMATA WHERE SCHEMA_NAME = ?", name).Scan(&actual); err != nil || actual != name {
				t.Fatalf("wizard created a different database: %q, %v", actual, err)
			}
			if strings.Contains(name, "`") {
				var changedCount int
				if err := db.QueryRow("SELECT COUNT(*) FROM information_schema.SCHEMATA WHERE SCHEMA_NAME = ?", strings.ReplaceAll(name, "`", "")).Scan(&changedCount); err != nil || changedCount != 0 {
					t.Fatalf("wizard also created the backtick-stripped name: %d, %v", changedCount, err)
				}
			}
		})
	}
}

func TestPingMySQLRejectsInvalidDatabaseNameBeforeConnecting(t *testing.T) {
	for _, name := range []string{"", "a\x00b", "a ", "😀", strings.Repeat("库", 65)} {
		if err := pingMySQL(mysqlInput{Host: "invalid.invalid", Port: 3306, DBName: name}); err == nil || err.Error() != "数据库名不合法" {
			t.Errorf("name %q reached connection instead of validation: %v", name, err)
		}
	}
}

func TestEnsureWritableDirPreservesExistingProbe(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".rferp-write-test")
	const original = "existing user content"
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	if err := ensureWritableDir(dir); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != original {
		t.Fatalf("existing probe changed: %q, %v", data, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != ".rferp-write-test" {
		t.Fatalf("temporary probe leaked: %v, %v", entries, err)
	}
}

func TestEnsureWritableDirCreatesAndCleansDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "new", "data")
	if err := ensureWritableDir(dir); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("probe not cleaned: %v, %v", entries, err)
	}
}

func TestEnsureWritableDirRejectsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(path, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := ensureWritableDir(path); err == nil {
		t.Fatal("regular file accepted as a directory")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "keep" {
		t.Fatalf("file changed: %q, %v", data, err)
	}
}

func TestEnsureWritableDirAcceptsLinkedRoot(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(t.TempDir(), "data")
	if runtime.GOOS == "windows" {
		if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, dir).CombinedOutput(); err != nil {
			t.Fatalf("create junction: %v: %s", err, out)
		}
	} else if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	if err := ensureWritableDir(link); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("probe leaked in linked directory: %v, %v", entries, err)
	}
}
