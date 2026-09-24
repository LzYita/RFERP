package repository

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/jmoiron/sqlx"
)

// VacuumInto writes a consistent full-file snapshot of the SQLite database
// using VACUUM INTO (D-014 single full snapshot).
func VacuumInto(db *sqlx.DB, destPath string) error {
	if db == nil {
		return fmt.Errorf("sqlite db is nil")
	}
	if destPath == "" {
		return fmt.Errorf("snapshot destination is empty")
	}
	q := fmt.Sprintf(`VACUUM INTO '%s'`, escapeSQLiteString(filepath.ToSlash(destPath)))
	_, err := db.Exec(q)
	if err != nil {
		return TranslateError(err)
	}
	return verifySQLiteDatabase(db)
}

// verifySQLiteDatabase runs a quick integrity probe after snapshot/restore.
func verifySQLiteDatabase(db *sqlx.DB) error {
	var res string
	if err := db.Get(&res, `PRAGMA integrity_check`); err != nil {
		return fmt.Errorf("snapshot integrity check: %w", err)
	}
	if res != "ok" {
		return fmt.Errorf("snapshot integrity check: %s", res)
	}
	return nil
}

// SnapshotSQLiteFile writes a timestamped VACUUM INTO snapshot of dbPath under saveDir.
func SnapshotSQLiteFile(dbPath, saveDir string) (string, error) {
	if err := os.MkdirAll(saveDir, 0o755); err != nil {
		return "", err
	}
	name := fmt.Sprintf("backup_%s.db", time.Now().Format("20060102_150405"))
	out := filepath.Join(saveDir, name)
	db, err := OpenSQLite(dbPath)
	if err != nil {
		return "", err
	}
	defer db.Close()
	if err := VacuumInto(db, out); err != nil {
		_ = os.Remove(out)
		return "", err
	}
	// Re-open snapshot and verify independently of the source connection.
	sdb, err := OpenSQLite(out)
	if err != nil {
		_ = os.Remove(out)
		return "", err
	}
	defer sdb.Close()
	if err := verifySQLiteDatabase(sdb); err != nil {
		_ = os.Remove(out)
		return "", err
	}
	return out, nil
}

func escapeSQLiteString(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\'' {
			out = append(out, '\'', '\'')
			continue
		}
		out = append(out, s[i])
	}
	return string(out)
}
