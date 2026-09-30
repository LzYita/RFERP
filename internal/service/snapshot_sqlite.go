package service

import (
	"app/internal/repository"
	"app/internal/usecase"
)

// NewSQLiteSnapshotPort snapshots and restores a SQLite database file via
// VACUUM INTO / file replace (D-014). dbPath is the Local data file.
// closer (optional) is invoked before restore so Windows releases the file.
func NewSQLiteSnapshotPort(dbPath string, closer func() error) usecase.SnapshotPort {
	return &sqliteSnapshotPort{dbPath: dbPath, closer: closer}
}

type sqliteSnapshotPort struct {
	dbPath string
	closer func() error
}

func (p *sqliteSnapshotPort) Snapshot(saveDir string) (string, error) {
	return repository.SnapshotSQLiteFile(p.dbPath, saveDir)
}

// PrefixSnapshot 生成 <prefix>_<时间戳>.db 的完整库文件副本（清空前留底）。
func (p *sqliteSnapshotPort) PrefixSnapshot(saveDir, prefix string) (string, error) {
	return repository.SnapshotSQLiteFileTo(p.dbPath, saveDir, prefix)
}

func (p *sqliteSnapshotPort) Kind() string {
	return usecase.SnapshotKindSQLite
}

func (p *sqliteSnapshotPort) Restore(snapshotPath string) (string, error) {
	// Verify the snapshot BEFORE dropping the live handle: a bad snapshot must
	// leave the running session intact, not hand back a closed database.
	if err := repository.VerifySQLiteFile(snapshotPath); err != nil {
		return "", err
	}
	if p.closer != nil {
		if err := p.closer(); err != nil {
			return "", err
		}
	}
	return repository.RestoreSQLiteFile(p.dbPath, snapshotPath)
}

var (
	_ usecase.SnapshotPort       = (*mysqlSnapshotPort)(nil)
	_ usecase.SnapshotPort       = (*sqliteSnapshotPort)(nil)
	_ usecase.PrefixSnapshotPort = (*sqliteSnapshotPort)(nil)
)
