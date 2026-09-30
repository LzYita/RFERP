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

// PrefixSnapshot 生成 <prefix>_<时间戳>.sql 的完整 dump。
// 不加任何 --ignore-table：这份副本是「被覆盖数据」的兜底，
// 必须包含 users / schema_migrations / db_identity，否则回退会丢掉系统表。
func (p *mysqlSnapshotPort) PrefixSnapshot(saveDir, prefix string) (string, error) {
	return dbbackup.BackupTo(p.dsn, p.tool, saveDir, prefix)
}

func (p *mysqlSnapshotPort) Kind() string {
	return usecase.SnapshotKindMySQL
}

// Restore is not used for MySQL SQL dumps: Service.RestoreDatabase rewrites the
// business tables from the dump inside one transaction. File-level replace does
// not apply to a live mysqldump snapshot.
func (p *mysqlSnapshotPort) Restore(snapshotPath string) (string, error) {
	return "", fmt.Errorf("MySQL 恢复请使用 SQL 备份导入（RestoreDatabase），不支持文件替换: %s", snapshotPath)
}

var _ usecase.PrefixSnapshotPort = (*mysqlSnapshotPort)(nil)
