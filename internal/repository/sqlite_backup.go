package repository

import (
	"fmt"
	"io"
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

// VerifySQLiteFile opens path and runs PRAGMA integrity_check. Callers use it to
// reject a bad snapshot before touching the live database.
func VerifySQLiteFile(path string) error {
	db, err := OpenSQLite(path)
	if err != nil {
		return fmt.Errorf("open snapshot: %w", err)
	}
	defer db.Close()
	if err := verifySQLiteDatabase(db); err != nil {
		return fmt.Errorf("snapshot verification failed: %w", err)
	}
	return nil
}

// removeSQLiteSidecars deletes the -wal / -shm files belonging to the database
// that was just replaced. A WAL left behind by an unclean shutdown would
// otherwise be replayed against the freshly restored file and corrupt it.
func removeSQLiteSidecars(dbPath string) {
	for _, suffix := range []string{"-wal", "-shm"} {
		_ = os.Remove(dbPath + suffix)
	}
}

// RestoreSQLiteFile replaces dbPath with the snapshot at snapshotPath (D-014
// single full rollback, no merge). Steps:
//  1. verify snapshot opens and passes integrity_check
//  2. if dbPath exists, keep it as <dbPath>.before-restore-<ts>
//  3. copy snapshot to dbPath via temp+rename, dropping stale WAL/SHM sidecars
//  4. verify the restored file; on failure put the previous file back
//
// Caller must close any live handles on dbPath first (Windows file locks).
func RestoreSQLiteFile(dbPath, snapshotPath string) (preRestoreBackup string, err error) {
	if dbPath == "" || snapshotPath == "" {
		return "", fmt.Errorf("db path and snapshot path are required")
	}
	// 1. verify snapshot independently
	if err := VerifySQLiteFile(snapshotPath); err != nil {
		return "", err
	}

	// 2. preserve current file
	if _, statErr := os.Stat(dbPath); statErr == nil {
		preRestoreBackup = fmt.Sprintf("%s.before-restore-%s", dbPath, time.Now().Format("20060102_150405"))
		if err := copyFile(dbPath, preRestoreBackup); err != nil {
			return "", fmt.Errorf("preserve current database: %w", err)
		}
	}

	// 3. temp + rename replace, then drop sidecars from the old database
	tmp := dbPath + ".restore-tmp"
	if err := copyFile(snapshotPath, tmp); err != nil {
		_ = os.Remove(tmp)
		return preRestoreBackup, fmt.Errorf("stage snapshot: %w", err)
	}
	if err := os.Rename(tmp, dbPath); err != nil {
		_ = os.Remove(tmp)
		return preRestoreBackup, fmt.Errorf("replace database file (close the app if it is running): %w", err)
	}
	removeSQLiteSidecars(dbPath)

	// 4. verify restored file
	rdb, err := OpenSQLite(dbPath)
	if err != nil {
		if preRestoreBackup != "" {
			_ = copyFile(preRestoreBackup, dbPath)
		}
		return preRestoreBackup, fmt.Errorf("open restored database: %w", err)
	}
	verr := verifySQLiteDatabase(rdb)
	rdb.Close()
	if verr != nil {
		if preRestoreBackup != "" {
			_ = copyFile(preRestoreBackup, dbPath)
		}
		return preRestoreBackup, fmt.Errorf("restored database verification failed: %w", verr)
	}
	return preRestoreBackup, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
