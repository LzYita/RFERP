package repository

import (
	"errors"
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
	return SnapshotSQLiteFileTo(dbPath, saveDir, "backup")
}

// SnapshotSQLiteFileTo 与 SnapshotSQLiteFile 相同，但文件名使用自定义前缀
// （前缀_时间戳.db）。供「破坏性操作前的自动副本」区分用途：
// pre_clear。产物仍是完整库文件，可直接用于另一次整库恢复。
func SnapshotSQLiteFileTo(dbPath, saveDir, prefix string) (string, error) {
	return snapshotSQLiteFileTo(dbPath, saveDir, prefix, time.Now())
}

func snapshotSQLiteFileTo(dbPath, saveDir, prefix string, at time.Time) (_ string, retErr error) {
	if prefix == "" {
		prefix = "backup"
	}
	if err := os.MkdirAll(saveDir, 0o755); err != nil {
		return "", err
	}
	name := fmt.Sprintf("%s_%s.db", prefix, at.Format("20060102_150405"))
	out := filepath.Join(saveDir, name)
	// VACUUM INTO accepts an empty file; reserve it so cleanup cannot delete an old backup.
	reserved, err := os.OpenFile(out, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", fmt.Errorf("reserve snapshot output: %w", err)
	}
	// shortcut: pathname cleanup assumes no replacement; use private staging for hostile local writers.
	defer func() {
		if retErr != nil {
			if err := os.Remove(out); err != nil && !os.IsNotExist(err) {
				retErr = errors.Join(retErr, fmt.Errorf("remove incomplete snapshot: %w", err))
			}
		}
	}()
	if err := reserved.Close(); err != nil {
		return "", fmt.Errorf("close snapshot reservation: %w", err)
	}
	db, err := OpenSQLite(dbPath)
	if err != nil {
		return "", err
	}
	defer db.Close()
	if err := VacuumInto(db, out); err != nil {
		return "", err
	}
	// Re-open snapshot and verify independently of the source connection.
	sdb, err := OpenSQLite(out)
	if err != nil {
		return "", err
	}
	defer sdb.Close()
	if err := verifySQLiteDatabase(sdb); err != nil {
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
	return restoreSQLiteFile(dbPath, snapshotPath, time.Now())
}

func restoreSQLiteFile(dbPath, snapshotPath string, at time.Time) (preRestoreBackup string, err error) {
	if dbPath == "" || snapshotPath == "" {
		return "", fmt.Errorf("db path and snapshot path are required")
	}
	// 1. verify snapshot independently
	if err := VerifySQLiteFile(snapshotPath); err != nil {
		return "", err
	}

	// 2. preserve current file
	if _, statErr := os.Stat(dbPath); statErr == nil {
		preRestoreBackup = fmt.Sprintf("%s.before-restore-%s", dbPath, at.Format("20060102_150405"))
		if err := copyFile(dbPath, preRestoreBackup, os.O_EXCL); err != nil {
			return "", fmt.Errorf("preserve current database: %w", err)
		}
	}

	// 3. temp + rename replace, then drop sidecars from the old database
	tmp := dbPath + ".restore-tmp"
	if err := copyFile(snapshotPath, tmp, os.O_EXCL); err != nil {
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
			_ = copyFile(preRestoreBackup, dbPath, os.O_TRUNC)
		}
		return preRestoreBackup, fmt.Errorf("open restored database: %w", err)
	}
	verr := verifySQLiteDatabase(rdb)
	rdb.Close()
	if verr != nil {
		if preRestoreBackup != "" {
			_ = copyFile(preRestoreBackup, dbPath, os.O_TRUNC)
		}
		return preRestoreBackup, fmt.Errorf("restored database verification failed: %w", verr)
	}
	return preRestoreBackup, nil
}

func copyFile(src, dst string, flag int) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|flag, 0o600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	err = errors.Join(copyErr, out.Close())
	if err != nil && flag&os.O_EXCL != 0 {
		if removeErr := os.Remove(dst); removeErr != nil && !os.IsNotExist(removeErr) {
			err = errors.Join(err, fmt.Errorf("remove incomplete copy: %w", removeErr))
		}
	}
	return err
}
