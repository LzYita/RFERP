package dbsetup

import (
	"database/sql"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
)

func TestQuoteDatabaseNamePreservesIdentifiers(t *testing.T) {
	cases := map[string]string{
		"app": "`app`", "ERPName": "`ERPName`", "erp-test": "`erp-test`", "数据库": "`数据库`",
		"a`b": "`a``b`", "a``b": "`a````b`", "中文-`库": "`中文-``库`",
		"a`; SELECT 1; --": "`a``; SELECT 1; --`", "a_%": "`a_%`",
		strings.Repeat("库", 64): "`" + strings.Repeat("库", 64) + "`",
	}
	for name, want := range cases {
		got, err := QuoteDatabaseName(name)
		if err != nil || got != want {
			t.Errorf("QuoteDatabaseName(%q) = %q, %v; want %q", name, got, err, want)
		}
	}
}

func TestInvalidDatabaseNamesRejectedBeforeConnecting(t *testing.T) {
	for _, name := range []string{"", "a\x00b", "a ", "😀", string([]byte{0xff}), strings.Repeat("a", 65), strings.Repeat("库", 65)} {
		quoted, err := QuoteDatabaseName(name)
		if err == nil || quoted != "" {
			t.Fatalf("invalid name %q produced usable identifier %q", name, quoted)
		}
		for _, err := range []error{EnsureDatabase("invalid DSN", name), EnsureUser("invalid DSN", "app", "unused", name)} {
			if err == nil || err.Error() != "数据库名不合法" {
				t.Errorf("name %q reached connection instead of validation: %v", name, err)
			}
		}
	}
	if err := EnsureUser("invalid DSN", "app'user", "unused", "app"); err == nil || err.Error() != "用户名或数据库名不合法" {
		t.Fatalf("account validation changed: %v", err)
	}
}

func TestGrantDatabaseNameEscapesOnlyPatternMode(t *testing.T) {
	for _, tc := range []struct{ quoted, pattern string }{
		{"`app`", "`app`"}, {"`app_unit%`", "`app\\_unit\\%`"},
		{"`中文_``库%`", "`中文\\_``库\\%`"}, {"`a\\b`", "`a\\\\b`"},
	} {
		if got := grantDatabaseName(tc.quoted, false); got != tc.pattern {
			t.Errorf("pattern quote = %q, want %q", got, tc.pattern)
		}
		if got := grantDatabaseName(tc.quoted, true); got != tc.quoted {
			t.Errorf("literal quote changed: %q, want %q", got, tc.quoted)
		}
	}
}

func TestMySQLDatabaseNamesAndExactGrants(t *testing.T) {
	dsn, dataDir := os.Getenv("RFERP_DBSETUP_TEST_DSN"), os.Getenv("RFERP_DBSETUP_TEST_DATADIR")
	if dsn == "" || dataDir == "" {
		t.Skip("requires an explicitly isolated MySQL instance")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	host, port, err := net.SplitHostPort(cfg.Addr)
	if err != nil || cfg.Net != "tcp" || host != "127.0.0.1" || port == "3306" || cfg.DBName != "" {
		t.Fatal("test DSN must select an isolated loopback port without a database")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var actualDir, originalMode string
	var originalPartial bool
	var lowerCaseNames int
	if err := db.QueryRow("SELECT @@datadir, @@global.sql_mode, @@global.partial_revokes, @@lower_case_table_names").Scan(&actualDir, &originalMode, &originalPartial, &lowerCaseNames); err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(filepath.Clean(actualDir), filepath.Clean(dataDir)) {
		t.Fatal("server data directory does not match the explicitly isolated directory")
	}
	defer func() {
		if _, err := db.Exec("SET GLOBAL sql_mode = ?", originalMode); err != nil {
			t.Error(err)
		}
		if _, err := db.Exec("SET GLOBAL partial_revokes = ?", originalPartial); err != nil {
			t.Error(err)
		}
	}()
	prefix := "sqlid_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	for _, partial := range []bool{false, true} {
		for _, mode := range []string{"", "NO_BACKSLASH_ESCAPES", "ANSI_QUOTES,NO_BACKSLASH_ESCAPES"} {
			t.Run(fmt.Sprintf("partial=%t/mode=%s", partial, mode), func(t *testing.T) {
				if _, err := db.Exec("SET GLOBAL partial_revokes = ?", partial); err != nil {
					t.Fatal(err)
				}
				if _, err := db.Exec("SET GLOBAL sql_mode = ?", mode); err != nil {
					t.Fatal(err)
				}
				for i, suffix := range []string{"-中文", "`库", "_unit", "%unit", "`; SELECT 1; --"} {
					t.Run(suffix, func(t *testing.T) {
						name := prefix + suffix
						quoted, err := QuoteDatabaseName(name)
						if err != nil {
							t.Fatal(err)
						}
						if err := EnsureDatabase(dsn, name); err != nil {
							t.Fatal(err)
						}
						t.Cleanup(func() {
							if _, err := db.Exec("DROP DATABASE " + quoted); err != nil {
								t.Error(err)
							}
						})
						var actual string
						want := name
						if lowerCaseNames == 1 {
							want = strings.ToLower(want)
						}
						if err := db.QueryRow("SELECT SCHEMA_NAME FROM information_schema.SCHEMATA WHERE SCHEMA_NAME = ?", name).Scan(&actual); err != nil || actual != want {
							t.Fatalf("database name changed: %q, %v", actual, err)
						}
						neighbor := strings.NewReplacer("_", "X", "%", "ZZ").Replace(name)
						neighborQuoted, err := QuoteDatabaseName(neighbor)
						if err != nil {
							t.Fatal(err)
						}
						if err := EnsureDatabase(dsn, neighbor); err != nil {
							t.Fatal(err)
						}
						t.Cleanup(func() {
							if _, err := db.Exec("DROP DATABASE " + neighborQuoted); err != nil {
								t.Error(err)
							}
						})
						for _, target := range []string{quoted, neighborQuoted} {
							if _, err := db.Exec("CREATE TABLE " + target + ".probe (id INT)"); err != nil {
								t.Fatal(err)
							}
						}
						user := fmt.Sprintf("%s%d", prefix, i)
						password, err := RandomPassword(24)
						if err != nil {
							t.Fatal(err)
						}
						t.Cleanup(func() {
							if _, err := db.Exec("DROP USER IF EXISTS '" + user + "'@'localhost'"); err != nil {
								t.Error(err)
							}
						})
						if err := EnsureUser(dsn, user, password, name); err != nil {
							t.Fatal(err)
						}
						clientCfg := *cfg
						clientCfg.User, clientCfg.Passwd = user, password
						client, err := sql.Open("mysql", clientCfg.FormatDSN())
						if err != nil {
							t.Fatal(err)
						}
						defer client.Close()
						var count int
						if err := client.QueryRow("SELECT COUNT(*) FROM " + quoted + ".probe").Scan(&count); err != nil || count != 0 {
							t.Fatalf("target database inaccessible: count=%d err=%v", count, err)
						}
						if _, err := client.Exec("INSERT INTO " + neighborQuoted + ".probe VALUES (1)"); err == nil {
							t.Fatal("grant leaked to a wildcard-matching neighboring database")
						} else if mysqlErr, ok := err.(*mysql.MySQLError); !ok || mysqlErr.Number != 1142 {
							t.Fatalf("neighbor denied for unexpected reason: %v", err)
						}
						if err := db.QueryRow("SELECT COUNT(*) FROM " + neighborQuoted + ".probe").Scan(&count); err != nil || count != 0 {
							t.Fatalf("neighbor changed despite rejection: count=%d err=%v", count, err)
						}
					})
				}
			})
		}
	}
}
