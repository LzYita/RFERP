package service

import (
	"app/internal/repository"
	"app/internal/usecase"
)

// NewSQLiteSnapshotPort snapshots a SQLite database file via VACUUM INTO.
// dbPath is the Local data file (e.g. <dataDir>/rferp.db).
func NewSQLiteSnapshotPort(dbPath string) usecase.SnapshotPort {
	return &sqliteSnapshotPort{dbPath: dbPath}
}

type sqliteSnapshotPort struct {
	dbPath string
}

func (p *sqliteSnapshotPort) Snapshot(saveDir string) (string, error) {
	return repository.SnapshotSQLiteFile(p.dbPath, saveDir)
}

var (
	_ usecase.SnapshotPort = (*mysqlSnapshotPort)(nil)
	_ usecase.SnapshotPort = (*sqliteSnapshotPort)(nil)
)
