package dbbackup

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
)

func Backup(dsn, mysqldumpPath, saveDir string) (string, error) {
	mc, err := mysql.ParseDSN(dsn)
	if err != nil {
		return "", fmt.Errorf("parse DSN: %w", err)
	}
	if mysqldumpPath == "" {
		mysqldumpPath = "mysqldump"
	}
	if err := os.MkdirAll(saveDir, 0755); err != nil {
		return "", fmt.Errorf("create backup dir: %w", err)
	}
	cfgFile, err := writeClientConfig(mc)
	if err != nil {
		return "", err
	}
	defer os.Remove(cfgFile)

	now := time.Now().Format("20060102_150405")
	filePath := filepath.Join(saveDir, fmt.Sprintf("backup_%s.sql", now))

	cmd := exec.Command(mysqldumpPath, "--defaults-extra-file="+cfgFile, mc.DBName)
	outFile, err := os.Create(filePath)
	if err != nil {
		return "", fmt.Errorf("create backup file: %w", err)
	}
	defer outFile.Close()
	cmd.Stdout = outFile
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("mysqldump failed: %w", err)
	}
	return filePath, nil
}

func writeClientConfig(mc *mysql.Config) (string, error) {
	f, err := os.CreateTemp("", "rferp-mysql-*.cnf")
	if err != nil {
		return "", fmt.Errorf("create temp config: %w", err)
	}
	name := f.Name()

	var b strings.Builder
	b.WriteString("[client]\n")
	b.WriteString("user=" + cnfQuote(mc.User) + "\n")
	b.WriteString("password=" + cnfQuote(mc.Passwd) + "\n")
	if host, port, ok := splitHostPort(mc.Addr); ok {
		b.WriteString("host=" + cnfQuote(host) + "\n")
		b.WriteString("port=" + cnfQuote(port) + "\n")
	}
	if _, err := f.WriteString(b.String()); err != nil {
		f.Close()
		os.Remove(name)
		return "", fmt.Errorf("write temp config: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(name)
		return "", fmt.Errorf("close temp config: %w", err)
	}
	return name, nil
}

func cnfQuote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}

func splitHostPort(addr string) (string, string, bool) {
	if addr == "" {
		return "", "", false
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr, "", false
	}
	return host, port, true
}
