package config

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"log"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/go-sql-driver/mysql"

	"app/internal/secret"
)

// DefaultUpdateURL is used when config.json does not specify updateUrl.
const DefaultUpdateURL = "https://github.com/LzYita/RFERP/releases/latest/download/releases.json"

type Config struct {
	DB            DBConfig
	Srv           ServerConfig
	DataDir       string
	MySQLService  string
	MysqldumpPath string
	UpdateURL     string
	AutoUpdate    bool

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

func (c *Config) SetDB(host string, port int, user, password, dbname string) {
	c.DB.Host = host
	c.DB.Port = port
	c.DB.User = user
	c.DB.Password = password
	c.DB.DBName = dbname
	c.DB.DSN = buildDSN(c.DB)
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
