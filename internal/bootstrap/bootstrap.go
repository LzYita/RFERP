// Package bootstrap 把「配置 → 数据库 → 迁移 → 用例」这段装配逻辑从各入口抽出来，
// 供 cmd/desktop（Wails 桌面壳）与 cmd/server 共用。
//
// 抽这层的原因：迁移前两处入口各自装配，桌面走 Fyne 的分支错误处理、server 直接
// log.Fatal，任何一侧新增存储通道或迁移步骤都要改两处，容易漂移。这里只做装配，
// 不做任何 UI 决策——错误原样返回，由调用方决定弹窗还是退出。
//
// 阶段 A 的硬性要求（见 docs/plans/wails-migration.md §Round 1）：内嵌服务必须同时
// 支持 SQLite 与 MySQL。SQLite 用户迁移后功能回退是不可接受的。
package bootstrap

import (
	"errors"
	"fmt"
	"strconv"

	// 两个存储通道的驱动都在本包注册：Build 是唯一发起连接的地方，
	// 入口只调用 Build，不该再各自 blank import。
	_ "github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"

	"app/internal/config"
	"app/internal/migrate"
	"app/internal/paths"
	"app/internal/repository"
	"app/internal/service"
	"app/internal/usecase"
)

// Kind 表示本次装配实际使用的存储通道。
type Kind string

const (
	KindSQLite Kind = "sqlite"
	KindMySQL  Kind = "mysql"
)

// Result 是装配成功后的产物。调用方负责在退出前调用 Close。
type Result struct {
	Apps    usecase.Applications
	Store   repository.Store
	Kind    Kind
	Target  string   // MySQL 为 DSN，SQLite 为数据库文件路径；用于日志与「连接服务器」页展示
	Applied []int    // 本次实际执行的迁移步骤，空表示已是最新
	Close   func() error
}

// NotConfigured 表示配置尚未完成（首启），而不是运行故障。
//
// 两者必须分开：配置缺失应引导用户走配置向导；配置齐全却连不上库是故障，
// 此时弹向导会让用户误以为配置丢了，甚至覆盖掉本来可用的配置。
// 区分依据是 cmd/desktop 既有的 cfg.Loaded() 语义。
type NotConfigured struct{ Err error }

func (e *NotConfigured) Error() string { return "not configured: " + e.Err.Error() }
func (e *NotConfigured) Unwrap() error { return e.Err }

// IsNotConfigured 判断错误是否属于「尚未配置」。
func IsNotConfigured(err error) bool {
	var e *NotConfigured
	return errors.As(err, &e)
}

// ConnectionError 表示配置齐全但连不上库。
//
// 同时携带拉起服务的错误：EnsureMySQL 失败**不会**终止尝试——服务名可能配错，
// 而 MySQL 其实已经在运行（cmd/desktop 一直按这个前提工作）。只有连接也失败时，
// 两个错误才一起交给调用方呈现。
type ConnectionError struct {
	EnsureErr  error // 拉起本机 MySQL 服务的错误；未尝试或已成功时为 nil
	ConnectErr error // 连接或迁移的错误
}

func (e *ConnectionError) Error() string {
	if e.EnsureErr != nil {
		return e.ConnectErr.Error() + "（启动服务亦失败：" + e.EnsureErr.Error() + "）"
	}
	return e.ConnectErr.Error()
}

func (e *ConnectionError) Unwrap() error { return e.ConnectErr }

// Options 控制装配里与运行环境相关的可选步骤。
type Options struct {
	// EnsureMySQL 在连库前拉起本机 MySQL 服务，仅桌面端需要（需 Windows 服务权限）。
	// cmd/server 面向已就绪的服务器，传 nil。
	EnsureMySQL func(host, port, service string) error

	// Logf 接收装配过程的进展日志；nil 时丢弃。
	Logf func(format string, args ...any)
}

func (o Options) logf(format string, args ...any) {
	if o.Logf != nil {
		o.Logf(format, args...)
	}
}

// Stage 标识装配失败发生在哪一步。界面据此选择不同的提示标题——
// 「无法打开数据库」和「数据库升级失败」对用户是完全不同的两件事，
// 合并成一句「启动失败」会让用户不知道该找谁。
type Stage string

const (
	StageSQLitePath    Stage = "sqlite-path"
	StageSQLiteOpen    Stage = "sqlite-open"
	StageSQLiteMigrate Stage = "sqlite-migrate"
	StageMySQLConnect  Stage = "mysql-connect"
	StageMySQLMigrate  Stage = "mysql-migrate"
)

// StageError 包装某个阶段的失败。
type StageError struct {
	Stage Stage
	Err   error
}

func (e *StageError) Error() string { return string(e.Stage) + ": " + e.Err.Error() }
func (e *StageError) Unwrap() error { return e.Err }

// StageOf 返回错误所属的阶段；不是阶段错误时返回空。
func StageOf(err error) Stage {
	var se *StageError
	if errors.As(err, &se) {
		return se.Stage
	}
	return ""
}

// Build 按 cfg 完成装配：选择存储通道 → 建立连接 → 执行迁移 → 构造用例。
//
// Build 不做重试、不弹窗、不退出进程；调用方拿到的 error 已经带有足够上下文，
// 可以直接呈现给用户。
func Build(cfg *config.Config, opts Options) (*Result, error) {
	if cfg.IsSQLite() {
		return buildSQLite(cfg, opts)
	}
	return buildMySQL(cfg, opts)
}

func buildSQLite(cfg *config.Config, opts Options) (*Result, error) {
	path, err := cfg.ResolveSQLitePath()
	if err != nil {
		return nil, &StageError{StageSQLitePath, fmt.Errorf("确定 SQLite 数据库路径: %w", err)}
	}
	opts.logf("bootstrap: storage=sqlite path=%s", path)

	db, err := repository.OpenSQLite(path)
	if err != nil {
		return nil, &StageError{StageSQLiteOpen, fmt.Errorf("打开本机数据库: %w", err)}
	}
	closeDB := func() error { return db.Close() }

	// 升级前快照：有 pending 步骤且库非空时先 VACUUM INTO 一份并校验，
	// 失败则中止迁移，不留「升级了一半」的库。
	res, err := migrate.RunSQLite(db, migrate.SQLiteOptions{
		DBPath:      path,
		SnapshotDir: paths.BackupDir(),
		Snapshot:    repository.SnapshotSQLiteFile,
	})
	if err != nil {
		_ = closeDB()
		return nil, &StageError{StageSQLiteMigrate, fmt.Errorf("本机数据库升级: %w", err)}
	}

	store := repository.NewSQLite(db)
	closer := closeDB
	apps := service.NewWithSnapshot(store, service.NewSQLiteSnapshotPort(path, closer), cfg)

	opts.logf("bootstrap: sqlite ready applied=%v", res.Applied)
	return &Result{
		Apps:    apps,
		Store:   store,
		Kind:    KindSQLite,
		Target:  path,
		Applied: res.Applied,
		Close:   closeDB,
	}, nil
}

func buildMySQL(cfg *config.Config, opts Options) (*Result, error) {
	if !cfg.Loaded() {
		return nil, &NotConfigured{Err: errors.New("尚未完成数据库配置")}
	}

	// 配置齐全却连不上是故障，不是「还没配置」——见 NotConfigured 的说明。
	//
	// 拉起服务失败不中断：服务名可能配错而 MySQL 已在运行，仍要再连一次。
	var ensureErr error
	if opts.EnsureMySQL != nil {
		if err := opts.EnsureMySQL(cfg.DB.Host, strconv.Itoa(cfg.DB.Port), cfg.MySQLService); err != nil {
			ensureErr = err
			opts.logf("bootstrap: ensure MySQL failed (will still try to connect): %v", err)
		}
	}
	opts.logf("bootstrap: storage=mysql dsn=%s", cfg.DB.DSN)

	db, err := sqlx.Connect("mysql", cfg.DB.DSN)
	if err != nil {
		return nil, &ConnectionError{
			EnsureErr:  ensureErr,
			ConnectErr: &StageError{StageMySQLConnect, fmt.Errorf("连接数据库: %w", err)},
		}
	}
	closeDB := func() error { return db.Close() }

	res, err := migrate.Run(db, migrate.Options{
		DSN:           cfg.DB.DSN,
		MysqldumpPath: cfg.MysqldumpPath,
		BackupDir:     paths.BackupDir(),
	})
	if err != nil {
		_ = closeDB()
		return nil, &ConnectionError{
			EnsureErr: ensureErr,
			ConnectErr: &StageError{StageMySQLMigrate,
				fmt.Errorf("数据库升级（升级前的备份如有已保留）: %w", err)},
		}
	}

	store := repository.New(db)
	apps := service.New(store, cfg.DB.DSN, cfg.MysqldumpPath, cfg)

	opts.logf("bootstrap: mysql ready applied=%v backup=%s", res.Applied, res.BackupPath)
	return &Result{
		Apps:    apps,
		Store:   store,
		Kind:    KindMySQL,
		Target:  cfg.DB.DSN,
		Applied: res.Applied,
		Close:   closeDB,
	}, nil
}
