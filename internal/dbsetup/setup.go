package dbsetup

import (
	"crypto/rand"
	"database/sql"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/go-sql-driver/mysql"
)

func EnsureDatabase(dsn, dbname string) error {
	name, err := QuoteDatabaseName(dbname)
	if err != nil {
		return err
	}
	db, err := openWithoutDB(dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	_, err = db.Exec("CREATE DATABASE IF NOT EXISTS " + name + " DEFAULT CHARSET utf8mb4")
	return err
}

func EnsureUser(adminDSN, user, password, dbname string) error {
	if !validIdent(user) {
		return fmt.Errorf("用户名或数据库名不合法")
	}
	name, err := QuoteDatabaseName(dbname)
	if err != nil {
		return err
	}
	db, err := openWithoutDB(adminDSN)
	if err != nil {
		return err
	}
	defer db.Close()
	var literalNames bool
	if strings.ContainsAny(dbname, `\_%`) {
		var variable, value string
		err := db.QueryRow("SHOW GLOBAL VARIABLES LIKE 'partial_revokes'").Scan(&variable, &value)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		literalNames = strings.EqualFold(value, "ON")
	}
	name = grantDatabaseName(name, literalNames)
	pw := strings.ReplaceAll(password, "'", "''")
	stmts := []string{
		fmt.Sprintf("CREATE USER IF NOT EXISTS '%s'@'localhost' IDENTIFIED BY '%s'", user, pw),
		fmt.Sprintf("ALTER USER '%s'@'localhost' IDENTIFIED BY '%s'", user, pw),
		fmt.Sprintf("GRANT SELECT,INSERT,UPDATE,DELETE,CREATE,ALTER,INDEX,DROP,REFERENCES ON %s.* TO '%s'@'localhost'", name, user),
		"FLUSH PRIVILEGES",
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return err
		}
	}
	return nil
}

// QuoteDatabaseName preserves a MySQL identifier; the server checks database-specific restrictions.
func QuoteDatabaseName(name string) (string, error) {
	if !utf8.ValidString(name) || name == "" || utf8.RuneCountInString(name) > 64 || strings.HasSuffix(name, " ") {
		return "", fmt.Errorf("数据库名不合法")
	}
	for _, r := range name {
		if r == 0 || r > 0xffff {
			return "", fmt.Errorf("数据库名不合法")
		}
	}
	return "`" + strings.ReplaceAll(name, "`", "``") + "`", nil
}

func grantDatabaseName(quoted string, literalNames bool) string {
	if literalNames {
		return quoted
	}
	// Database-level grants use wildcard patterns unless partial_revokes is enabled.
	return strings.NewReplacer(`\`, `\\`, `_`, `\_`, `%`, `\%`).Replace(quoted)
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

func validIdent(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_') {
			return false
		}
	}
	return true
}
