package service

import (
	"fmt"

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

// Restore is not used for MySQL SQL dumps: Service.RestoreDatabase executes
// the dump's INSERT statements in one transaction. File-level replace does
// not apply to a live mysqldump snapshot.
func (p *mysqlSnapshotPort) Restore(snapshotPath string) (string, error) {
	return "", fmt.Errorf("MySQL 恢复请使用 SQL 备份导入（RestoreDatabase），不支持文件替换: %s", snapshotPath)
}
