package repository

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/jmoiron/sqlx"
)

func timeNow() time.Time      { return time.Now() }
func osRemove(p string) error { return os.Remove(p) }

// VacuumInto writes a consistent full-file snapshot of the SQLite database
// at dbPath into destPath using VACUUM INTO (D-014 single full snapshot).
func VacuumInto(db *sqlx.DB, destPath string) error {
	if db == nil {
		return fmt.Errorf("sqlite db is nil")
	}
	if destPath == "" {
		return fmt.Errorf("snapshot destination is empty")
	}
	// VACUUM INTO does not accept parameters; quote the path as a SQL string.
	q := fmt.Sprintf(`VACUUM INTO '%s'`, escapeSQLiteString(filepath.ToSlash(destPath)))
	_, err := db.Exec(q)
	return err
}

func vacuumIntoFile(dbPath, destPath string) error {
	db, err := OpenSQLite(dbPath)
	if err != nil {
		return err
	}
	defer db.Close()
	return VacuumInto(db, destPath)
}

// SnapshotSQLiteFile writes a timestamped VACUUM INTO snapshot of dbPath under saveDir.
func SnapshotSQLiteFile(dbPath, saveDir string) (string, error) {
	if err := mkdirAll(saveDir); err != nil {
		return "", err
	}
	name := fmt.Sprintf("backup_%s.db", timeNow().Format("20060102_150405"))
	out := filepath.Join(saveDir, name)
	if err := vacuumIntoFile(dbPath, out); err != nil {
		_ = osRemove(out)
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
