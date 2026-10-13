package usecase

// 适配端口：库差异与平台差异只出现在实现侧（MySQL / 将来 SQLite 等）。
// Service 只依赖本文件中的接口，不依赖具体驱动或命令行工具。

// 快照后端标识。Service 用 Kind 拒绝跨后端的恢复（不能把 MySQL dump 灌进
// SQLite，也不能把 SQLite 整库快照当 SQL 备份追加导入）。
const (
	SnapshotKindMySQL  = "mysql"
	SnapshotKindSQLite = "sqlite"
)

// SnapshotPort 备份快照端口。语义见 D-014：
// 每次 Snapshot 产出某一时刻的独立完整快照；Restore 将目标恢复为该快照整库状态；
// 不做新旧备份合成。
type SnapshotPort interface {
	// Snapshot 在 saveDir 下生成完整快照文件，返回其路径。
	Snapshot(saveDir string) (path string, err error)
	// Restore 将当前库整库替换为 snapshotPath 指向的快照（不合并）。
	// 实现应在替换前保留当前状态，并在失败时回滚到替换前。
	Restore(snapshotPath string) (preRestoreBackup string, err error)
	// Kind 返回本端口对应的存储后端（SnapshotKindMySQL / SnapshotKindSQLite）。
	Kind() string
}

// PrefixSnapshotPort 是 SnapshotPort 的可选扩展：生成带自定义前缀的完整快照。
// 用途是「破坏性操作（整库恢复 / 清空数据）前自动留副本」——
// 与用户手动「一键备份」区分开，便于识别与按份数清理。
// 实现应保证产物仍是完整快照，可直接作为另一次恢复的输入。
type PrefixSnapshotPort interface {
	PrefixSnapshot(saveDir, prefix string) (path string, err error)
}

// BackupInspection 是一份备份「能不能安全回滚」的预检结论（见 dbbackup.Coverage）。
//
// 之所以要把它送到界面上：恢复的实现是「先 DELETE 全部业务表，再回灌 INSERT」，
// 所以一份早于某张表引入的备份，恢复后那张表会被清空且无处回填。
// 用户在输完口令、点完确认之后才发现，是最晚的发现时机。
type BackupInspection struct {
	// SchemaKnown 为 false 表示预检只读到文件开头、没读到结尾，
	// 无法断言覆盖度。界面此时必须说「无法确认」，不得显示缺哪几张表。
	SchemaKnown bool
	// MissingTables：当前业务表里、这份备份的 schema 中没有的表。
	MissingTables []string
	// Usable 为 false 时不可作为恢复来源（典型：非 UTF-8 编码）。
	Usable bool
	// Reason 是不可用的中文原因，直接展示给用户。
	Reason string
}

// BackupInspector 是「预检一份备份文件」的可选端口。
//
// 用可选端口而不是加进 Applications：备份列表是只读的增强，
// 没实现它时列表照常工作（前端把覆盖度当「无法确认」处理），
// 而不是让所有实现方与测试桩都必须补一个方法。
type BackupInspector interface {
	InspectBackupFile(path string) (BackupInspection, error)
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

// ServerInfo 描述「当前绑定的数据库是谁」（D-016）。
// 本机模式由本机库回答，客户端模式由服务器回答；两者用同一结构，才能互相比较。
type ServerInfo struct {
	AppVersion    string `json:"appVersion"`
	APIVersion    int    `json:"apiVersion"`
	SchemaVersion int    `json:"schemaVersion"`
	DatabaseID    string `json:"databaseId"`
	Storage       string `json:"storage"`
}

// ServerDescriptor 让「我当前绑定的数据库」可被自描述。
//
// 用途（本机 → 服务器的前期准备，D-016）：
//   - 接入服务器前预检：比较本机库与目标服务器库是不是同一个
//     （同一个 → 切运行模式不必迁移数据；不同 → 必须先搬数据）
//   - 备份/恢复校验快照归属
//
// 实现不得依赖登录态：客户端模式下也应当能问出来。
type ServerDescriptor interface {
	Describe() (ServerInfo, error)
}
