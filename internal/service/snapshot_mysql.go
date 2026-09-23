package service

import (
	"app/internal/dbbackup"
	"app/internal/usecase"
)

// mysqlSnapshotPort 将 dump 快照限制在适配边界内（D-013）。
type mysqlSnapshotPort struct {
	dsn  string
	tool string
}

func newMySQLSnapshotPort(dsn, tool string) usecase.SnapshotPort {
	if tool == "" {
		tool = "mysqldump"
	}
	return &mysqlSnapshotPort{dsn: dsn, tool: tool}
}

func (p *mysqlSnapshotPort) Snapshot(saveDir string) (string, error) {
	return dbbackup.Backup(p.dsn, p.tool, saveDir)
}
