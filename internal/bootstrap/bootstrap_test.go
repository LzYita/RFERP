package bootstrap

import (
	"errors"
	"path/filepath"
	"testing"

	"app/internal/config"
	"app/internal/paths"
)

// newSQLiteCfg 造一份最小可用的 SQLite 配置。
// mode=local + storage=sqlite 可避开任何外部依赖，因此这些用例不需要 MySQL 也能在 CI 跑。
func newSQLiteCfg(t *testing.T, dir string) *config.Config {
	t.Helper()
	return &config.Config{
		Mode:       config.ModeLocal,
		Storage:    config.StorageSQLite,
		DataDir:    dir,
		SQLitePath: filepath.Join(dir, "rferp.db"),
	}
}

// newLoadedMySQLCfg 造一份「已配置」的 MySQL 配置。
//
// 必须真的走 config.Save()：Config.Loaded() 读的是未导出的 path 字段，
// 只有 Save/Load 会设置它，结构体字面量永远不算「已配置」。
// AppData 重定向到临时目录，避免写到用户真实的 %APPDATA%\RFERP\config.json。
func newLoadedMySQLCfg(t *testing.T, dir string) *config.Config {
	t.Helper()
	t.Setenv("AppData", dir)
	cfg := &config.Config{
		Mode:    config.ModeLocal,
		Storage: config.StorageMySQL,
		DataDir: dir,
		DB: config.DBConfig{
			Host: "127.0.0.1", Port: 1, User: "u", Password: "p", DBName: "d",
			DSN: "u:p@tcp(127.0.0.1:1)/d",
		},
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("保存测试配置: %v", err)
	}
	if !cfg.Loaded() {
		t.Fatal("Save() 之后 Loaded() 仍为 false，测试前提不成立")
	}
	return cfg
}

func TestBuildSQLiteProducesUsableApplications(t *testing.T) {
	dir := t.TempDir()
	paths.SetDataDir(dir)
	cfg := newSQLiteCfg(t, dir)

	var logged int
	res, err := Build(cfg, Options{Logf: func(string, ...any) { logged++ }})
	if err != nil {
		t.Fatalf("Build(sqlite): %v", err)
	}
	t.Cleanup(func() { _ = res.Close() })

	if res.Kind != KindSQLite {
		t.Errorf("Kind = %q, want %q", res.Kind, KindSQLite)
	}
	if res.Apps == nil {
		t.Fatal("Apps 为 nil：装配成功就必须给出可用的用例集合")
	}
	if res.Store == nil {
		t.Fatal("Store 为 nil")
	}
	if res.Close == nil {
		t.Fatal("Close 为 nil：调用方依赖它释放数据库句柄")
	}
	if logged == 0 {
		t.Error("Logf 未被调用：装配过程应当可观测")
	}
	if res.Applied == nil {
		t.Error("Applied 为 nil：首启应当报告实际执行的迁移步骤")
	}
	if filepath.Base(res.Target) != "rferp.db" {
		t.Errorf("Target = %q，SQLite 通道应回报数据库文件路径", res.Target)
	}
}

// 二次装配必须幂等：同一份配置再 Build 一次不应重复执行迁移。
// 这是桌面端「升级后重启」与 server/desktop 共用装配的基本要求。
func TestBuildSQLiteIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	paths.SetDataDir(dir)
	cfg := newSQLiteCfg(t, dir)

	first, err := Build(cfg, Options{})
	if err != nil {
		t.Fatalf("first Build: %v", err)
	}
	if len(first.Applied) == 0 {
		t.Fatal("首启应当执行迁移")
	}
	_ = first.Close()

	second, err := Build(cfg, Options{})
	if err != nil {
		t.Fatalf("second Build: %v", err)
	}
	defer second.Close()

	if len(second.Applied) != 0 {
		t.Errorf("二次装配又执行了迁移 %v：迁移应当幂等", second.Applied)
	}
}

// MySQL 且尚未配置时，必须返回 NotConfigured 而不是普通错误：
// 调用方据此引导配置向导，而不是报「数据库不可用」。
func TestBuildMySQLUnconfiguredIsNotConfigured(t *testing.T) {
	dir := t.TempDir()
	paths.SetDataDir(dir)
	cfg := &config.Config{Mode: config.ModeLocal, Storage: config.StorageMySQL, DataDir: dir}

	_, err := Build(cfg, Options{})
	if err == nil {
		t.Fatal("未配置 MySQL 时 Build 应当失败")
	}
	if !IsNotConfigured(err) {
		t.Errorf("IsNotConfigured = false, err = %v；未配置必须与「连不上库」区分开", err)
	}
	var nc *NotConfigured
	if !errors.As(err, &nc) {
		t.Errorf("errors.As(*NotConfigured) = false, err = %v", err)
	}
}

// 未配置时不应先去拉 MySQL 服务：没配置就没有服务名可拉。
func TestBuildMySQLUnconfiguredSkipsEnsure(t *testing.T) {
	dir := t.TempDir()
	paths.SetDataDir(dir)
	cfg := &config.Config{Mode: config.ModeLocal, Storage: config.StorageMySQL, DataDir: dir}

	called := 0
	_, _ = Build(cfg, Options{EnsureMySQL: func(string, string, string) error { called++; return nil }})

	if called != 0 {
		t.Errorf("EnsureMySQL 被调用 %d 次；未配置时不应尝试拉服务", called)
	}
}

// EnsureMySQL 失败不得中断装配：服务名可能配错而 MySQL 已在运行，
// 仍要再连一次。这是 cmd/desktop 一直依赖的行为，抽装配时不能丢掉。
func TestEnsureMySQLFailureDoesNotAbortAttempt(t *testing.T) {
	dir := t.TempDir()
	paths.SetDataDir(dir)
	cfg := newLoadedMySQLCfg(t, dir)

	ensureErr := errors.New("未配置 MySQL 服务名")
	called := 0
	_, err := Build(cfg, Options{
		EnsureMySQL: func(string, string, string) error { called++; return ensureErr },
	})

	if called != 1 {
		t.Errorf("EnsureMySQL 调用次数 = %d, want 1", called)
	}
	// 连 127.0.0.1:1 必然失败，重点是错误里要同时带上两个原因。
	if err == nil {
		t.Fatal("应当连接失败")
	}
	var ce *ConnectionError
	if !errors.As(err, &ce) {
		t.Fatalf("err 不是 ConnectionError: %v", err)
	}
	if !errors.Is(ce.EnsureErr, ensureErr) {
		t.Errorf("EnsureErr = %v, want %v；界面需要同时呈现「连不上」和「服务没起来」", ce.EnsureErr, ensureErr)
	}
	if ce.ConnectErr == nil {
		t.Error("ConnectErr 为 nil")
	}
}

// EnsureMySQL 成功但连不上时，错误里不应出现「启动服务亦失败」。
func TestConnectionErrorOmitsEnsureWhenItSucceeded(t *testing.T) {
	dir := t.TempDir()
	paths.SetDataDir(dir)
	cfg := newLoadedMySQLCfg(t, dir)

	_, err := Build(cfg, Options{EnsureMySQL: func(string, string, string) error { return nil }})

	var ce *ConnectionError
	if !errors.As(err, &ce) {
		t.Fatalf("err 不是 ConnectionError: %v", err)
	}
	if ce.EnsureErr != nil {
		t.Errorf("EnsureErr = %v, want nil；服务已成功拉起不应再作为失败原因呈现", ce.EnsureErr)
	}
}
