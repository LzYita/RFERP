package dbsetup

import (
	"crypto/rand"
	"database/sql"
	"fmt"
	"regexp"
	"strings"

	"github.com/go-sql-driver/mysql"
)

func EnsureDatabase(dsn, dbname string) error {
	if !ValidIdentifier(dbname) {
		return fmt.Errorf("数据库名不合法")
	}
	db, err := openWithoutDB(dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	_, err = db.Exec("CREATE DATABASE IF NOT EXISTS `" + dbname + "` DEFAULT CHARSET utf8mb4")
	return err
}

func EnsureUser(adminDSN, user, password, dbname string) error {
	if !ValidIdentifier(user) || !ValidIdentifier(dbname) {
		return fmt.Errorf("用户名或数据库名不合法")
	}
	db, err := openWithoutDB(adminDSN)
	if err != nil {
		return err
	}
	defer db.Close()
	pw := strings.ReplaceAll(password, "'", "''")
	stmts := []string{
		fmt.Sprintf("CREATE USER IF NOT EXISTS '%s'@'localhost' IDENTIFIED BY '%s'", user, pw),
		fmt.Sprintf("ALTER USER '%s'@'localhost' IDENTIFIED BY '%s'", user, pw),
		fmt.Sprintf("GRANT SELECT,INSERT,UPDATE,DELETE,CREATE,ALTER,INDEX,DROP,REFERENCES ON `%s`.* TO '%s'@'localhost'", dbname, user),
		"FLUSH PRIVILEGES",
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return err
		}
	}
	return nil
}

func RandomPassword(n int) (string, error) {
	const chars = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	for i := range b {
		b[i] = chars[int(b[i])%len(chars)]
	}
	return string(b), nil
}

func openWithoutDB(dsn string) (*sql.DB, error) {
	mc, err := mysql.ParseDSN(dsn)
	if err != nil {
		return nil, err
	}
	mc.DBName = ""
	db, err := sql.Open("mysql", mc.FormatDSN())
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

var identifierPattern = regexp.MustCompile(`^[A-Za-z0-9_]{1,64}$`)

// ValidIdentifier restricts interpolated MySQL identifiers to the supported names.
func ValidIdentifier(s string) bool { return identifierPattern.MatchString(s) }
