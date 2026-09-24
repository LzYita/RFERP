package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-sql-driver/mysql"
)

func TestLegacyDSNMigrationEncryptsPassword(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	legacy := `{"dsn":"root:secretpw@tcp(127.0.0.1:3306)/appliancedb?charset=utf8mb4&parseTime=true&loc=Local"}`
	if err := os.WriteFile(path, []byte(legacy), 0600); err != nil {
		t.Fatal(err)
	}

	fc := &fileConfig{}
	if err := json.Unmarshal([]byte(legacy), fc); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{}
	applyFile(cfg, fc)
	if cfg.DB.Password != "secretpw" {
		t.Fatalf("parsed password = %q", cfg.DB.Password)
	}
	if cfg.DB.User != "root" || cfg.DB.DBName != "appliancedb" {
		t.Fatalf("parsed fields: user=%q db=%q", cfg.DB.User, cfg.DB.DBName)
	}

	if err := migrate(path, cfg); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out fileConfig
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	if out.PasswordEnc == "" {
		t.Fatal("password_enc is empty")
	}
	if out.Password != "" || out.DSN != "" {
		t.Fatal("plaintext password/dsn retained after migration")
	}
	if got := decodePassword(&out); got != "secretpw" {
		t.Fatalf("decoded password = %q", got)
	}
}

func TestBuildDSNHandlesSpecialChars(t *testing.T) {
	db := DBConfig{Host: "127.0.0.1", Port: 3306, User: "mes_app", Password: `p@ss:w/rd?#x`, DBName: "appliancedb"}
	mc, err := mysql.ParseDSN(buildDSN(db))
	if err != nil {
		t.Fatal(err)
	}
	if mc.Passwd != db.Password {
		t.Fatalf("password round-trip = %q, want %q", mc.Passwd, db.Password)
	}
	if mc.User != db.User || mc.DBName != db.DBName {
		t.Fatalf("fields: user=%q db=%q", mc.User, mc.DBName)
	}
	if mc.Addr != "127.0.0.1:3306" {
		t.Fatalf("addr = %q", mc.Addr)
	}
}

func TestStorageDefaultsToMySQLAndE6NoSilentSwitch(t *testing.T) {
	// v1.1 style config without storage → MySQL (never silent SQLite).
	fc := &fileConfig{Host: "127.0.0.1", Port: 3306, User: "u", DBName: "d"}
	cfg := &Config{}
	applyFile(cfg, fc)
	if cfg.StorageKind() != StorageMySQL {
		t.Fatalf("empty storage kind = %q, want mysql", cfg.StorageKind())
	}
	if cfg.IsSQLite() {
		t.Fatal("legacy config must not become SQLite silently")
	}

	// Explicit sqlite only when set.
	cfg2 := &Config{}
	applyFile(cfg2, &fileConfig{Storage: "sqlite", DataDir: `E:\data`})
	if !cfg2.IsSQLite() {
		t.Fatal("explicit storage=sqlite should be SQLite")
	}
	if got := cfg2.ResolveSQLitePath(); got != filepath.Join(`E:\data`, DefaultSQLiteFileName) {
		t.Fatalf("sqlite path = %q", got)
	}

	// Client mode never counts as local SQLite.
	cfg3 := &Config{Mode: ModeClient, Storage: StorageSQLite}
	if cfg3.IsSQLite() {
		t.Fatal("client mode must not use local sqlite")
	}
}

func TestSetStoragePersistsExplicitChoice(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"host":"127.0.0.1","port":3306,"user":"u","dbname":"d"}`), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{path: path, Storage: StorageMySQL}
	if err := cfg.SetStorage(StorageSQLite, filepath.Join(dir, "rferp.db")); err != nil {
		t.Fatalf("SetStorage: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out fileConfig
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	if out.Storage != StorageSQLite {
		t.Fatalf("persisted storage = %q", out.Storage)
	}
	// Data is not migrated by SetStorage — only config is written.
}
