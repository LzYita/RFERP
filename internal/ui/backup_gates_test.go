package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fyne.io/fyne/v2/test"

	"app/internal/auth"
	"app/internal/model"
	"app/internal/usecase"
)

// stubApps 满足 usecase.Applications（未实现的方法由嵌入的 nil 接口兜底，
// 闸门测试不会走到那些方法），只记录 Backup 域的调用。
type stubApps struct {
	usecase.Applications
	restoreCalls int
	clearCalls   int
	restoreErr   error
}

func (s *stubApps) DataDir() string { return t_tmpDir }

func (s *stubApps) SetDataDir(string) error { return nil }

func (s *stubApps) BackupDatabase(string) (string, error) { return "", nil }

func (s *stubApps) RestoreDatabase(filePath string) (usecase.RestoreResult, error) {
	s.restoreCalls++
	if s.restoreErr != nil {
		return usecase.RestoreResult{}, s.restoreErr
	}
	return usecase.RestoreResult{
		Statements:  3,
		PreRestore:  "pre_restore.sql",
		TableCounts: map[string]int{"parts": 2},
	}, nil
}

func (s *stubApps) ClearDatabase() (usecase.ClearResult, error) {
	s.clearCalls++
	return usecase.ClearResult{PreClear: "pre_clear.sql", Cleared: []string{"parts"}}, nil
}

// t_tmpDir 只是 DataDir 的占位值；闸门测试不会真的写文件。
const t_tmpDir = "C:\\rferp-test-datadir"

func writeTmp(t *testing.T, dir, name string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("-- Dump completed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func asAdmin(t *testing.T) {
	t.Helper()
	prev := auth.Current()
	auth.SetCurrent(&model.User{Role: string(auth.RoleAdmin)})
	t.Cleanup(func() { auth.SetCurrent(prev) })
}

func newGatesScreen() (*BackupScreen, *stubApps) {
	stub := &stubApps{}
	return NewBackupScreen(stub, test.NewWindow(nil)), stub
}

// TestRestoreGateErrBlocksMistakenRestores 逐条验证闸门 G1/G2：
// 任何"选错文件 / 口令不对"的组合都不得进入确认流程。
func TestRestoreGateErrBlocksMistakenRestores(t *testing.T) {
	dir := t.TempDir()
	dump := filepath.Join(dir, "backup_20260925_101530.sql")
	if err := os.WriteFile(dump, []byte("-- Dump completed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	empty := filepath.Join(dir, "backup_20260925_101531.sql")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	unnamed := filepath.Join(dir, "random.sql")
	if err := os.WriteFile(unnamed, []byte("-- Dump completed\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name      string
		path      string
		token     string
		wantAllow bool
	}{
		{"token matches the file timestamp", dump, "restore 20260925_101530", true},
		{"token not typed", dump, "", false},
		{"token belongs to a different backup", dump, "restore 20260101_000000", false},
		{"token with stray whitespace", dump, "  restore 20260925_101530  ", false},
		{"empty file is refused", empty, "restore 20260925_101531", false},
		{"file without a recognisable timestamp", unnamed, "restore 20260925_101530", false},
		{"missing file", filepath.Join(dir, "nope.sql"), "restore 20260925_101530", false},
		{"no path at all", "", "restore 20260925_101530", false},
	}
	for _, tc := range cases {
		gateErr := restoreGateErr(tc.path, tc.token)
		if tc.wantAllow && gateErr != nil {
			t.Errorf("%s: expected the gates to pass, got %v", tc.name, gateErr)
		}
		if !tc.wantAllow && gateErr == nil {
			t.Errorf("%s: the gates should have refused this restore", tc.name)
		}
	}
}

// TestRestoreGateErrMessagesNameTheProblem 拒绝时必须说清原因，
// 而不是只给一句"失败"。
func TestRestoreGateErrMessagesNameTheProblem(t *testing.T) {
	dir := t.TempDir()
	empty := filepath.Join(dir, "backup_20260101_000000.sql")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"prompt":    restoreGateErr("", "x").title,
		"file":      restoreGateErr(filepath.Join(dir, "nope.sql"), "x").title,
		"invalid":   restoreGateErr(empty, "restore 20260101_000000").title,
		"tokenless": restoreGateErr(writeTmp(t, dir, "unnamed.sql"), "x").title,
	}
	want := map[string]string{
		"prompt":    "提示",
		"file":      "提示",
		"invalid":   "备份文件无效",
		"tokenless": "无法确认",
	}
	for key, got := range cases {
		if got != want[key] {
			t.Errorf("%s gate title = %q, want %q", key, got, want[key])
		}
	}
}

// TestBackupScreenClearGate 口令不对时不得触达 service。
func TestBackupScreenClearGate(t *testing.T) {
	test.NewApp()
	asAdmin(t)

	screen, stub := newGatesScreen()
	screen.Build()

	screen.clearConfirm.SetText("drop please")
	screen.doClear()
	if stub.clearCalls != 0 {
		t.Errorf("a wrong confirmation must not reach the service, calls=%d", stub.clearCalls)
	}
}

// TestBackupScreenDisablesButtonsWhileRunning 防重复提交。
func TestBackupScreenDisablesButtonsWhileRunning(t *testing.T) {
	test.NewApp()
	asAdmin(t)

	screen, _ := newGatesScreen()
	screen.Build()

	if screen.importBtn.Disabled() || screen.clearBtn.Disabled() {
		t.Fatal("buttons should start enabled")
	}
	screen.setRunning(true)
	if !screen.importBtn.Disabled() || !screen.clearBtn.Disabled() {
		t.Error("both destructive buttons must be disabled while an operation runs")
	}
	if !screen.running {
		t.Error("running flag should be set")
	}
	screen.setRunning(false)
	if screen.importBtn.Disabled() || screen.clearBtn.Disabled() {
		t.Error("buttons must be re-enabled afterwards")
	}
}

// TestBackupScreenTokenIsPrefilledOnSelection 选中文件后自动填口令；
// 换成无法识别的文件时必须清空，不能留上一次的旧口令。
func TestBackupScreenTokenIsPrefilledOnSelection(t *testing.T) {
	test.NewApp()
	asAdmin(t)

	screen, _ := newGatesScreen()
	screen.Build()

	screen.importPath.SetText(`D:\备份\backup_20260101_000000.sql`)
	screen.refreshRestorePrompt()
	if got := screen.restoreToken.Text; got != "restore 20260101_000000" {
		t.Errorf("token = %q, want it prefilled from the selected file", got)
	}

	screen.importPath.SetText(`D:\备份\some.sql`)
	screen.refreshRestorePrompt()
	if got := strings.TrimSpace(screen.restoreToken.Text); got != "" {
		t.Errorf("token = %q, want empty for an unrecognised file", got)
	}
}

func TestRestoreRowsSummaryIsSortedAndStable(t *testing.T) {
	got := restoreRowsSummary(map[string]int{"parts": 210, "audit_log": 397, "products": 70})
	want := "audit_log=397\nparts=210\nproducts=70"
	if got != want {
		t.Errorf("restoreRowsSummary = %q, want %q", got, want)
	}
}
