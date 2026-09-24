package usecase

// 适配端口：库差异与平台差异只出现在实现侧（MySQL / 将来 SQLite 等）。
// Service 只依赖本文件中的接口，不依赖具体驱动或命令行工具。

// SnapshotPort 备份快照端口。语义见 D-014：
// 每次 Snapshot 产出某一时刻的独立完整快照；Restore 将目标恢复为该快照整库状态；
// 不做新旧备份合成。
type SnapshotPort interface {
	// Snapshot 在 saveDir 下生成完整快照文件，返回其路径。
	Snapshot(saveDir string) (path string, err error)
	// Restore 将当前库整库替换为 snapshotPath 指向的快照（不合并）。
	// 实现应在替换前保留当前状态，并在失败时回滚到替换前。
	Restore(snapshotPath string) (preRestoreBackup string, err error)
}

// SessionPort 在“关闭外键/唯一检查”的会话中执行 operation，用于批量导入/清库。
// MySQL 实现使用会话级 FOREIGN_KEY_CHECKS / UNIQUE_CHECKS；
// SQLite 实现应使用 PRAGMA 等价物或逐语句策略。Service 不得出现这些方言。
type SessionPort interface {
	WithBulkLoad(operation func(ex Execer) error) error
}

// Execer 批量载入期间可用的最小执行面（由适配层提供事务/连接）。
// 这里只声明能力边界；具体 SQL 仍在适配层构造的调用方（如恢复解析器）中执行。
type Execer interface {
	Exec(query string, args ...any) error
}

// MigratorPort 版本化迁移入口。当前实现为 internal/migrate（MySQL）。
type MigratorPort interface {
	// Run 应用待执行迁移；升级前备份策略由实现或调用方保证。
	Run() error
}
