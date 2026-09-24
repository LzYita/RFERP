package config

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"

	"app/internal/secret"
)

// DefaultUpdateURL is used when config.json does not specify updateUrl.
// It goes through a GitHub accelerator; update.DefaultFallbackURL is tried if
// the accelerator is unreachable.
const DefaultUpdateURL = "https://ghfast.top/https://github.com/LzYita/RFERP/releases/latest/download/releases.json"

const (
	// ModeLocal 桌面直连业务内核（本机/已配 MySQL）。
	ModeLocal = "local"
	// ModeClient 连接局域网主机上的 API，不直连业务库。
	ModeClient = "client"
)

const (
	// StorageMySQL Local 使用 MySQL（默认；v1.1 及更早唯一形态）。
	StorageMySQL = "mysql"
	// StorageSQLite Local 使用数据目录下的 SQLite 文件（Phase E / D-013）。
	StorageSQLite = "sqlite"
)

// DefaultSQLiteFileName is the Local SQLite file name under DataDir.
const DefaultSQLiteFileName = "rferp.db"

type Config struct {
	DB            DBConfig
	Srv           ServerConfig
	DataDir       string
	MySQLService  string
	MysqldumpPath string
	UpdateURL     string
	AutoUpdate    bool
	// Mode 为 ModeLocal 或 ModeClient；空视为未选择（需首启）。
	Mode string
	// ServerURL 仅 ModeClient：API 根地址，如 http://192.168.1.10:8080
	ServerURL string
	// Storage 为 StorageMySQL 或 StorageSQLite；空视为 MySQL（E6：不静默切库）。
	// 仅 ModeLocal 使用。
	Storage string
	// SQLitePath 仅 StorageSQLite：数据库文件路径；空则用 DataDir/rferp.db。
	SQLitePath string

	path string
}

type DBConfig struct {
	Host     string
	Port     int
	User     string
	Password string
	DBName   string
	DSN      string
}

type ServerConfig struct {
	Addr string
}

type fileConfig struct {
	DSN           string `json:"dsn,omitempty"`
	Host          string `json:"host"`
	Port          int    `json:"port"`
	User          string `json:"user"`
	Password      string `json:"password,omitempty"`
	PasswordEnc   string `json:"password_enc,omitempty"`
	DBName        string `json:"dbname"`
	DataDir       string `json:"dataDir,omitempty"`
	MySQLService  string `json:"mysqlService,omitempty"`
	MysqldumpPath string `json:"mysqldumpPath,omitempty"`
	UpdateURL     string `json:"updateUrl,omitempty"`
	AutoUpdate    *bool  `json:"autoUpdate,omitempty"`
	Mode          string `json:"mode,omitempty"`
	ServerURL     string `json:"serverUrl,omitempty"`
	Storage       string `json:"storage,omitempty"`
	SQLitePath    string `json:"sqlitePath,omitempty"`
}

func Load() *Config {
	path, fc := readConfigFile()

	cfg := &Config{AutoUpdate: true}
	if fc != nil {
		applyFile(cfg, fc)
	} else if dsn := os.Getenv("DB_DSN"); dsn != "" {
		if mc, err := mysql.ParseDSN(dsn); err == nil {
			applyMySQLConfig(cfg, mc)
		}
	}

	if cfg.DB.Host == "" {
		cfg.DB.Host = "127.0.0.1"
	}
	if cfg.DB.Port == 0 {
		cfg.DB.Port = 3306
	}
	if cfg.DB.User == "" {
		cfg.DB.User = "root"
	}
	if cfg.DB.DBName == "" {
		cfg.DB.DBName = "appliancedb"
	}
	if cfg.MySQLService == "" {
		cfg.MySQLService = "MySQL80"
	}
	if cfg.UpdateURL == "" {
		cfg.UpdateURL = DefaultUpdateURL
	}
	cfg.Srv.Addr = os.Getenv("SRV_ADDR")
	if cfg.Srv.Addr == "" {
		cfg.Srv.Addr = ":8080"
	}
	cfg.Storage = normalizeStorage(cfg.Storage)
	cfg.DB.DSN = buildDSN(cfg.DB)
	cfg.path = path

	if fc != nil && path != "" && needsMigration(fc) {
		if err := migrate(path, cfg); err != nil {
			log.Printf("config migrate warning: %v", err)
		}
	}
	return cfg
}

func applyFile(cfg *Config, fc *fileConfig) {
	if fc.Host != "" {
		cfg.DB.Host = fc.Host
		cfg.DB.Port = fc.Port
		cfg.DB.User = fc.User
		cfg.DB.DBName = fc.DBName
		cfg.DB.Password = decodePassword(fc)
	} else if fc.DSN != "" {
		if mc, err := mysql.ParseDSN(fc.DSN); err == nil {
			applyMySQLConfig(cfg, mc)
		} else {
			log.Printf("config.json dsn parse error: %v", err)
		}
	}
	cfg.DataDir = fc.DataDir
	cfg.MySQLService = fc.MySQLService
	cfg.MysqldumpPath = fc.MysqldumpPath
	cfg.UpdateURL = fc.UpdateURL
	cfg.Mode = fc.Mode
	cfg.ServerURL = fc.ServerURL
	cfg.Storage = normalizeStorage(fc.Storage)
	cfg.SQLitePath = fc.SQLitePath
	if fc.AutoUpdate != nil {
		cfg.AutoUpdate = *fc.AutoUpdate
	}
}

func applyMySQLConfig(cfg *Config, mc *mysql.Config) {
	cfg.DB.User = mc.User
	cfg.DB.Password = mc.Passwd
	cfg.DB.DBName = mc.DBName
	host, port := splitAddr(mc.Addr)
	cfg.DB.Host = host
	cfg.DB.Port = port
}

func splitAddr(addr string) (string, int) {
	if addr == "" {
		return "127.0.0.1", 3306
	}
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return addr, 3306
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port == 0 {
		port = 3306
	}
	return host, port
}

func buildDSN(db DBConfig) string {
	mc := mysql.NewConfig()
	mc.User = db.User
	mc.Passwd = db.Password
	mc.Net = "tcp"
	mc.Addr = net.JoinHostPort(db.Host, strconv.Itoa(db.Port))
	mc.DBName = db.DBName
	mc.ParseTime = true
	mc.Loc = time.Local
	mc.Timeout = 5 * time.Second
	mc.Params = map[string]string{"charset": "utf8mb4"}
	return mc.FormatDSN()
}

func decodePassword(fc *fileConfig) string {
	if fc.PasswordEnc == "" {
		return fc.Password
	}
	raw, err := base64.StdEncoding.DecodeString(fc.PasswordEnc)
	if err != nil {
		log.Printf("decode password_enc error: %v", err)
		return ""
	}
	plain, err := secret.Unprotect(raw)
	if err != nil {
		log.Printf("decrypt db password error: %v", err)
		return ""
	}
	return string(plain)
}

func needsMigration(fc *fileConfig) bool {
	if fc.PasswordEnc != "" {
		return false
	}
	return fc.DSN != "" || fc.Password != ""
}

func migrate(path string, cfg *Config) error {
	if err := writeEncrypted(path, cfg); err != nil {
		return err
	}
	log.Printf("config.json migrated to encrypted format")
	return nil
}

func writeEncrypted(path string, cfg *Config) error {
	enc, err := secret.Protect([]byte(cfg.DB.Password))
	if err != nil {
		return err
	}
	if back, err := secret.Unprotect(enc); err != nil || string(back) != cfg.DB.Password {
		return err
	}

	out := fileConfig{
		Host:          cfg.DB.Host,
		Port:          cfg.DB.Port,
		User:          cfg.DB.User,
		PasswordEnc:   base64.StdEncoding.EncodeToString(enc),
		DBName:        cfg.DB.DBName,
		DataDir:       cfg.DataDir,
		MySQLService:  cfg.MySQLService,
		MysqldumpPath: cfg.MysqldumpPath,
		UpdateURL:     cfg.UpdateURL,
		AutoUpdate:    &cfg.AutoUpdate,
		Mode:          cfg.Mode,
		ServerURL:     cfg.ServerURL,
		Storage:       cfg.Storage,
		SQLitePath:    cfg.SQLitePath,
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

func (c *Config) Loaded() bool {
	return c.path != ""
}

// ModeChosen 是否已在首启选定运行模式（D3）。
func (c *Config) ModeChosen() bool {
	return c.Mode == ModeLocal || c.Mode == ModeClient
}

// SetRunMode 持久化运行模式。Client 模式保存服务器地址；Local 保留库配置。
func (c *Config) SetRunMode(mode, serverURL string) error {
	switch mode {
	case ModeLocal:
		c.Mode = ModeLocal
		c.ServerURL = ""
	case ModeClient:
		u := strings.TrimSpace(serverURL)
		if u == "" {
			return fmt.Errorf("服务器地址不能为空")
		}
		c.Mode = ModeClient
		c.ServerURL = strings.TrimRight(u, "/")
	default:
		return fmt.Errorf("未知运行模式: %s", mode)
	}
	return c.Save()
}

func (c *Config) SetDB(host string, port int, user, password, dbname string) {
	c.DB.Host = host
	c.DB.Port = port
	c.DB.User = user
	c.DB.Password = password
	c.DB.DBName = dbname
	c.DB.DSN = buildDSN(c.DB)
}

func normalizeStorage(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case StorageSQLite:
		return StorageSQLite
	case StorageMySQL, "":
		return StorageMySQL
	default:
		return StorageMySQL
	}
}

// StorageKind 返回 Local 存储类型。空/未知一律 MySQL（E6 禁止静默切库）。
func (c *Config) StorageKind() string {
	return normalizeStorage(c.Storage)
}

// IsSQLite 是否使用本地 SQLite 文件（仅 Local 形态）。
func (c *Config) IsSQLite() bool {
	return c.Mode != ModeClient && c.StorageKind() == StorageSQLite
}

// ResolveSQLitePath 返回 SQLite 文件完整路径；未配置时为 DataDir/rferp.db。
// DataDir 与 sqlitePath 均为空时报错，绝不落到进程 CWD（D-002）。
func (c *Config) ResolveSQLitePath() (string, error) {
	if p := strings.TrimSpace(c.SQLitePath); p != "" {
		return p, nil
	}
	base := strings.TrimSpace(c.DataDir)
	if base == "" {
		return "", fmt.Errorf("DataDir 未配置，无法确定 SQLite 路径（请设置 dataDir 或 sqlitePath）")
	}
	return filepath.Join(base, DefaultSQLiteFileName), nil
}

// SetStorage 显式切换 Local 存储（E6）。不迁移业务数据，仅改配置。
// 从 MySQL 切到 SQLite 或反向都必须由用户在 UI 确认后调用本方法。
func (c *Config) SetStorage(kind, sqlitePath string) error {
	prevStorage, prevSQLitePath := c.Storage, c.SQLitePath
	switch normalizeStorage(kind) {
	case StorageSQLite:
		c.Storage = StorageSQLite
		if sqlitePath != "" {
			c.SQLitePath = sqlitePath
		}
		if _, err := c.ResolveSQLitePath(); err != nil {
			// 校验失败时回滚内存状态，避免与磁盘上的配置不一致。
			c.Storage, c.SQLitePath = prevStorage, prevSQLitePath
			return err
		}
	case StorageMySQL:
		c.Storage = StorageMySQL
		c.SQLitePath = ""
	default:
		return fmt.Errorf("未知存储类型: %s", kind)
	}
	return c.Save()
}

func (c *Config) Save() error {
	p := c.path
	if p == "" || !dirWritable(filepath.Dir(p)) {
		p = UserConfigPath()
	}
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return err
	}
	if err := writeEncrypted(p, c); err != nil {
		return err
	}
	c.path = p
	return nil
}

func UserConfigPath() string {
	dir, err := os.UserConfigDir()
	if err != nil || dir == "" {
		return "config.json"
	}
	return filepath.Join(dir, "RFERP", "config.json")
}

func dirWritable(dir string) bool {
	f, err := os.CreateTemp(dir, ".wtest")
	if err != nil {
		return false
	}
	name := f.Name()
	f.Close()
	os.Remove(name)
	return true
}

func readConfigFile() (string, *fileConfig) {
	for _, p := range candidatePaths() {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
		var fc fileConfig
		if err := json.Unmarshal(data, &fc); err != nil {
			log.Printf("config.json parse error (%s): %v", p, err)
			continue
		}
		return p, &fc
	}
	return "", nil
}

func candidatePaths() []string {
	var out []string
	if exe, err := os.Executable(); err == nil {
		out = append(out, filepath.Join(filepath.Dir(exe), "config.json"))
	}
	if wd, err := os.Getwd(); err == nil {
		out = append(out, filepath.Join(wd, "config.json"))
	}
	out = append(out, UserConfigPath())
	return out
}
