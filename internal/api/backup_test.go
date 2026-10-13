package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"app/internal/paths"
	"app/internal/usecase"
)

// Round 5：备份 / 恢复 / 清空 / 数据目录端点的关口。
//
// 三类断言，各自对应一类会被静默放过的错误：
//
//	破坏性闸门   —— 本机形态之外不能做（阶段 B/C 的能力矩阵）
//	路径穿越     —— 客户端只能给文件名，不能给路径
//	三分类       —— rolled_back / unknown / applied 必须在响应里可程序化区分

// backupApps 只实现被测端点用到的用例，其余靠嵌入接口为 nil（真被调用会 panic，
// 那是好事：说明测试没注意到新增依赖）。
type backupApps struct {
	usecase.Applications

	dataDir   string
	restored  string
	cleared   bool
	restoreFn func(string) (usecase.RestoreResult, error)
	clearFn   func() (usecase.ClearResult, error)
}

func (a *backupApps) DataDir() string { return a.dataDir }
func (a *backupApps) SetDataDir(dir string) error {
	a.dataDir = dir
	return nil
}
func (a *backupApps) BackupDatabase(saveDir string) (string, error) {
	return filepath.Join(saveDir, "backup_20260925_101530.sql"), nil
}
func (a *backupApps) RestoreDatabase(file string) (usecase.RestoreResult, error) {
	a.restored = file
	if a.restoreFn != nil {
		return a.restoreFn(file)
	}
	return usecase.RestoreResult{Statements: 42, PreRestore: filepath.Join(paths.BackupDir(), "pre_restore_20260929_110000.sql")}, nil
}
func (a *backupApps) ClearDatabase() (usecase.ClearResult, error) {
	a.cleared = true
	if a.clearFn != nil {
		return a.clearFn()
	}
	return usecase.ClearResult{
		PreClear: filepath.Join(paths.BackupDir(), "pre_clear_20260929_105500.sql"),
		Cleared:  []string{"parts", "products"},
	}, nil
}

// newBackupServer 建服务器，并把数据目录指到临时目录。
func newBackupServer(t *testing.T, apps *backupApps, opts ...Option) *Server {
	t.Helper()
	dir := t.TempDir()
	paths.SetDataDir(dir)
	apps.dataDir = dir
	t.Cleanup(func() { paths.SetDataDir("") })
	return New(apps, opts...)
}

// seedBackupFile 在备份目录里放一份可被 restore 解析到的备份文件。
//
// restore 的第一道闸是 resolveBackupFile：文件不在备份目录就直接拒、用例根本不被
// 调用。三分类的用例必须先让这一关过掉，才测得到状态映射。
func seedBackupFile(t *testing.T, name string) {
	t.Helper()
	dir := paths.BackupDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte("-- Dump completed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// issueAdmin 见 dto_test.go：同包已有的 helper，直接复用。

func doJSON(t *testing.T, s *Server, method, url, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, url, rdr)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	return w
}

// --------------------------------------------------------------- 本机闸门

// 阶段 B/C：远程部署时破坏性操作必须被挡住，且**不能碰用例**。
func TestDestructiveEndpointsRequireHostOperations(t *testing.T) {
	cases := []struct {
		method string
		url    string
		body   any
	}{
		{"POST", "/api/restore", map[string]string{"file": "backup_20260925_101530.sql"}},
		{"POST", "/api/clear", map[string]string{"confirm": "drop"}},
		{"POST", "/api/datadir", map[string]string{"dir": "D:/somewhere"}},
	}
	for _, c := range cases {
		t.Run(c.method+" "+c.url, func(t *testing.T) {
			apps := &backupApps{}
			// **不传** WithHostOperations —— 默认必须是不放行。
			s := newBackupServer(t, apps)
			tok := issueAdmin(t, s)
			w := doJSON(t, s, c.method, c.url, tok, c.body)
			if w.Code != http.StatusForbidden {
				t.Errorf("远程形态下 %s %s = %d，期望 403", c.method, c.url, w.Code)
			}
			if apps.restored != "" || apps.cleared {
				t.Errorf("%s %s 在被拒绝时仍然调用了用例——闸门位置不对", c.method, c.url)
			}
		})
	}
}

func TestDestructiveEndpointsWorkOnHost(t *testing.T) {
	apps := &backupApps{}
	s := newBackupServer(t, apps, WithHostOperations())
	seedBackupFile(t, "backup_20260925_101530.sql")
	tok := issueAdmin(t, s)

	if w := doJSON(t, s, "POST", "/api/restore", tok, map[string]string{"file": "backup_20260925_101530.sql"}); w.Code != http.StatusOK {
		t.Fatalf("本机形态恢复 = %d, body=%s", w.Code, w.Body.String())
	}
	if apps.restored == "" {
		t.Error("恢复没有调用用例")
	}
	// 副本路径不得以绝对路径形式回给客户端
	var out map[string]any
	if err := json.Unmarshal(doJSON(t, s, "POST", "/api/restore", tok,
		map[string]string{"file": "backup_20260925_101530.sql"}).Body.Bytes(), &out); err != nil {
		t.Fatalf("解析响应: %v", err)
	}
	pre, _ := out["preRestore"].(string)
	if strings.ContainsAny(pre, `/\`) {
		t.Errorf("preRestore 回传了路径：%q（应只给文件名）", pre)
	}
	if pre != "pre_restore_20260929_110000.sql" {
		t.Errorf("preRestore = %q, want 文件名", pre)
	}

	if w := doJSON(t, s, "POST", "/api/clear", tok, map[string]string{"confirm": "drop"}); w.Code != http.StatusOK {
		t.Fatalf("本机形态清空 = %d, body=%s", w.Code, w.Body.String())
	}
}

// --------------------------------------------------- 原生目录选择框（宿主注入）
//
// 这一组守的是方案丙的边界：picker 是宿主能力，没注入的形态要**明确**告诉前端
// "弹不了，请手填"，而不是 500 或静默空串——界面靠这个提示决定要不要把输入框亮出来。

func TestPickDataDirRequiresHostOperations(t *testing.T) {
	apps := &backupApps{}
	// 不传 WithHostOperations：远程形态连框都不该弹。
	s := newBackupServer(t, apps,
		WithDirectoryPicker(DirectoryPickerFunc(func(string) (string, error) {
			t.Error("被本机闸门挡下时仍然弹了框")
			return "", nil
		})))
	tok := issueAdmin(t, s)
	if w := doJSON(t, s, "POST", "/api/datadir/pick", tok, nil); w.Code != http.StatusForbidden {
		t.Errorf("远程形态下选目录 = %d，期望 403", w.Code)
	}
}

func TestPickDataDirReturnsChosenPath(t *testing.T) {
	want := `D:\仓库数据`
	apps := &backupApps{}
	var gotCurrent string
	s := newBackupServer(t, apps,
		WithHostOperations(),
		WithDirectoryPicker(DirectoryPickerFunc(func(current string) (string, error) {
			gotCurrent = current
			return want, nil
		})))
	tok := issueAdmin(t, s)

	w := doJSON(t, s, "POST", "/api/datadir/pick", tok, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("选目录 = %d, body=%s", w.Code, w.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("解析响应: %v", err)
	}
	if out["dir"] != want {
		t.Errorf("dir = %v, want %q", out["dir"], want)
	}
	// 框应当从当前目录打开，用户才容易在邻近位置挑，而不是从盘符根开始找。
	if gotCurrent != apps.dataDir {
		t.Errorf("弹框时的当前目录 = %q, want %q", gotCurrent, apps.dataDir)
	}
}

// 用户取消是正常结果：200 + 空串。界面据此保持原状，不能报错弹窗。
func TestPickDataDirCancelIsNotAnError(t *testing.T) {
	apps := &backupApps{}
	s := newBackupServer(t, apps,
		WithHostOperations(),
		WithDirectoryPicker(DirectoryPickerFunc(func(string) (string, error) { return "", nil })))
	tok := issueAdmin(t, s)

	w := doJSON(t, s, "POST", "/api/datadir/pick", tok, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("取消选目录 = %d, body=%s", w.Code, w.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("解析响应: %v", err)
	}
	if out["dir"] != "" {
		t.Errorf("取消时 dir = %v, want 空串", out["dir"])
	}
}

// cmd/server 与浏览器形态：没有窗口可弹。必须是可识别的 501 + 中文提示，
// 前端据此回退到文本输入框。
func TestPickDataDirWithoutPickerIs501(t *testing.T) {
	apps := &backupApps{}
	s := newBackupServer(t, apps, WithHostOperations()) // 不注入 picker
	tok := issueAdmin(t, s)

	w := doJSON(t, s, "POST", "/api/datadir/pick", tok, nil)
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("无 picker 时 = %d, body=%s，期望 501", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "手动输入") {
		t.Errorf("501 响应没有告诉用户怎么继续：%s", w.Body.String())
	}
}

// 弹框失败（WebView2 异常等）不该让用户以为是自己输错了：500 + 同样可行动的中文。
func TestPickDataDirHostErrorIs500(t *testing.T) {
	apps := &backupApps{}
	s := newBackupServer(t, apps,
		WithHostOperations(),
		WithDirectoryPicker(DirectoryPickerFunc(func(string) (string, error) {
			return "", errors.New("webview2 not available")
		})))
	tok := issueAdmin(t, s)

	w := doJSON(t, s, "POST", "/api/datadir/pick", tok, nil)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("弹框失败 = %d, body=%s，期望 500", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "手动输入") {
		t.Errorf("500 响应没有告诉用户怎么继续：%s", w.Body.String())
	}
}

// 选目录只是挑一个路径，**不**写配置。写入由 SetDataDir 负责。
func TestPickDataDirDoesNotChangeDataDir(t *testing.T) {
	apps := &backupApps{}
	s := newBackupServer(t, apps,
		WithHostOperations(),
		WithDirectoryPicker(DirectoryPickerFunc(func(string) (string, error) {
			return `D:\选中的但还没确认的目录`, nil
		})))
	tok := issueAdmin(t, s)
	before := apps.dataDir

	if w := doJSON(t, s, "POST", "/api/datadir/pick", tok, nil); w.Code != http.StatusOK {
		t.Fatalf("选目录 = %d, body=%s", w.Code, w.Body.String())
	}
	if apps.dataDir != before {
		t.Errorf("选目录顺手改了数据目录：%q -> %q", before, apps.dataDir)
	}
}

// 权限点之外还要校验口令：绕过界面直接调端点不能清库。
func TestClearRequiresConfirmToken(t *testing.T) {
	apps := &backupApps{}
	s := newBackupServer(t, apps, WithHostOperations())
	tok := issueAdmin(t, s)
	for _, bad := range []string{"", "drop ", " DROP", "dro", "删除"} {
		w := doJSON(t, s, "POST", "/api/clear", tok, map[string]string{"confirm": bad})
		if w.Code != http.StatusBadRequest {
			t.Errorf("口令 %q 返回 %d，期望 400", bad, w.Code)
		}
		if apps.cleared {
			t.Fatalf("口令 %q 竟然通过了清空", bad)
		}
	}
}

// --------------------------------------------------------------- 路径穿越

// 客户端只能给备份目录内的**文件名**。任何路径形式都必须被拒。
//
// 这一条是 Round 5 新增能力的头号风险点：此前没有恢复端点，所以没有"客户端指
// 认任意文件"的入口。现在有了，就必须把入口收紧。
func TestRestoreRejectsPathsOutsideBackupDir(t *testing.T) {
	apps := &backupApps{}
	s := newBackupServer(t, apps, WithHostOperations())
	tok := issueAdmin(t, s)

	// 先在备份目录外放一个"看起来能恢复"的文件，证明不是"文件不存在"挡住的
	outside := filepath.Join(t.TempDir(), "secret.sql")
	if err := os.WriteFile(outside, []byte("-- Dump completed\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	bad := []string{
		"",
		"..",
		"..\\..\\" + filepath.Base(outside),
		filepath.Join("..", filepath.Base(outside)),
		"..\\..\\..\\Windows\\System32\\config\\SAM",
		outside, // 绝对路径
		"C:\\Windows\\System32\\config\\SAM",
		"sub\\dir.sql",
	}
	for _, name := range bad {
		w := doJSON(t, s, "POST", "/api/restore", tok, map[string]string{"file": name})
		if w.Code == http.StatusOK {
			t.Errorf("文件名 %q 被接受了（%d）——存在路径穿越", name, w.Code)
		}
		if apps.restored != "" {
			t.Fatalf("文件名 %q 竟然调用了 RestoreDatabase(%q)", name, apps.restored)
		}
	}
}

// ------------------------------------------------- 真实备份文件名（含 .final/.upgraded）
//
// 这一组是回归：早先的规则只认结尾 `.sql`，而 D:\仓库数据 里躺着
// backup_20260924_170830.final.sql 与 .upgraded.sql（迁移期间手工改名留下的），
// 于是**旧备份一个都恢复不了**，界面只回一句「请选择本应用生成的文件」。
//
// 这些文件名此前只在前端被校验，服务端 resolveBackupFile 只防目录穿越、不看格式：
// 界面拒绝的文件直接调 API 又能恢复。所以这里验的是**服务端**这一侧。

func TestRestoreAcceptsRealWorldBackupNames(t *testing.T) {
	for _, name := range []string{
		"backup_20260721_154617.sql",
		"backup_20260924_170830.sql",
		"backup_20260924_170830.final.sql",
		"backup_20260924_170830.upgraded.sql",
		"pre_restore_20260924_172919.sql",
		"backup_20260924_170830.final.db",
	} {
		t.Run(name, func(t *testing.T) {
			apps := &backupApps{}
			s := newBackupServer(t, apps, WithHostOperations())
			seedBackupFile(t, name)
			tok := issueAdmin(t, s)

			if w := doJSON(t, s, "POST", "/api/restore", tok, map[string]string{"file": name}); w.Code != http.StatusOK {
				t.Errorf("真实备份 %q 被拒：%d %s", name, w.Code, w.Body.String())
			}
			if apps.restored == "" {
				t.Errorf("真实备份 %q 没有调用 RestoreDatabase", name)
			}
		})
	}
}

// 反向：备份目录里确实存在、但命名不合规的文件，服务端也必须拒。
//
// 文件真实存在这一点很重要——否则测试可能只是被「文件不存在」挡住的，
// 看起来通过，实际没测到新加的那道校验。
func TestRestoreRejectsFilesWithoutRecoverableTimestamp(t *testing.T) {
	for _, name := range []string{
		"notes.txt",
		"audit_log_20260721_20260721.csv",
		"backup_20260925_101530.sql.bak",
		"backup.sql",
		"backup_20260925.sql",
		"manual_dump.sql",
	} {
		t.Run(name, func(t *testing.T) {
			apps := &backupApps{}
			s := newBackupServer(t, apps, WithHostOperations())
			seedBackupFile(t, name) // 文件确实存在
			tok := issueAdmin(t, s)

			w := doJSON(t, s, "POST", "/api/restore", tok, map[string]string{"file": name})
			if w.Code == http.StatusOK {
				t.Fatalf("%q 被接受了（%d）——命名不合规的文件也能恢复", name, w.Code)
			}
			if apps.restored != "" {
				t.Fatalf("%q 竟然调用了 RestoreDatabase(%q)", name, apps.restored)
			}
			if !strings.Contains(w.Body.String(), "时间戳") {
				t.Errorf("%q 的拒绝理由没提到时间戳，用户会看不懂：%s", name, w.Body.String())
			}
		})
	}
}

// --------------------------------------------------------------- 三分类

// 三种结局必须在响应里可程序化区分——界面要靠它决定用黄色还是红色、
// 渲不渲染「重试」按钮。塌缩成一句中文就等于这个能力不存在。
func TestDestructiveOutcomeStatesAreDistinguishable(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		wantState string
		wantCode  int
	}{
		{
			name:      "普通错误=已回滚，可重试",
			err:       errors.New("备份文件校验未通过，已中止恢复"),
			wantState: OpStateRolledBack,
			wantCode:  http.StatusBadRequest,
		},
		{
			name: "提交回滚结果不明=不要重试",
			err: &usecase.OperationError{
				State: usecase.OperationUnknown,
				Err:   errors.New("COMMIT failed"),
			},
			wantState: OpStateUnknown,
			wantCode:  http.StatusInternalServerError,
		},
		{
			name: "已提交仅收尾失败=重试等于做两遍",
			err: &usecase.OperationError{
				State: usecase.OperationApplied,
				Err:   errors.New("audit insert failed"),
			},
			wantState: OpStateApplied,
			wantCode:  http.StatusInternalServerError,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			apps := &backupApps{restoreFn: func(string) (usecase.RestoreResult, error) {
				return usecase.RestoreResult{}, c.err
			}}
			s := newBackupServer(t, apps, WithHostOperations())
			seedBackupFile(t, "backup_20260925_101530.sql")
			tok := issueAdmin(t, s)
			w := doJSON(t, s, "POST", "/api/restore", tok,
				map[string]string{"file": "backup_20260925_101530.sql"})
			if w.Code != c.wantCode {
				t.Errorf("HTTP = %d, want %d, body=%s", w.Code, c.wantCode, w.Body.String())
			}
			var out struct {
				Error string `json:"error"`
				State string `json:"state"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
				t.Fatalf("响应不是 JSON：%v（body=%s）", err, w.Body.String())
			}
			if out.State != c.wantState {
				t.Errorf("state = %q, want %q", out.State, c.wantState)
			}
			if out.Error == "" {
				t.Error("error 文案为空")
			}
			// 「不要重试」只对 unknown/applied 成立；rolled_back 必须说清楚可以再来。
			// 匹配「不要重试」或「不要直接重试」两种写法（applied 用的是后者）。
			saysNoRetry := strings.Contains(out.Error, "不要重试") || strings.Contains(out.Error, "不要直接重试")
			if c.wantState == OpStateRolledBack && strings.Contains(out.Error, "不要重试") {
				t.Errorf("已回滚的文案不该说「不要重试」：%q", out.Error)
			}
			if c.wantState != OpStateRolledBack && !saysNoRetry {
				t.Errorf("%s 的文案必须写明「不要重试」：%q", c.wantState, out.Error)
			}
		})
	}
}

// 内部错误（可能是 SQL 错误）不得回给客户端。
func TestDestructiveErrorDoesNotLeakInternalDetail(t *testing.T) {
	apps := &backupApps{restoreFn: func(string) (usecase.RestoreResult, error) {
		return usecase.RestoreResult{}, errors.New(
			"Error 1146: Table 'appliancedb.parts' doesn't exist near 'DELETE FROM parts'")
	}}
	s := newBackupServer(t, apps, WithHostOperations())
	seedBackupFile(t, "backup_20260925_101530.sql")
	tok := issueAdmin(t, s)
	w := doJSON(t, s, "POST", "/api/restore", tok, map[string]string{"file": "backup_20260925_101530.sql"})
	body := w.Body.String()
	for _, leak := range []string{"1146", "appliancedb", "DELETE FROM"} {
		if strings.Contains(body, leak) {
			t.Errorf("响应泄露了内部细节 %q：%s", leak, body)
		}
	}
}

// --------------------------------------------------------------- 备份下载

// 备份下载必须回**文件内容**，不能回服务器绝对路径。
//
// 这是 §4.5 缺口 5 明确记下的信息泄露：原实现返回 {"path": "D:\\仓库数据\\备份\\x.sql"}，
// 既泄露服务器目录布局，浏览器也拿不到文件本身。
func TestBackupDownloadReturnsStreamNotServerPath(t *testing.T) {
	apps := &backupApps{}
	s := newBackupServer(t, apps)
	tok := issueAdmin(t, s)

	// 让 BackupDatabase 落到真实文件上，才能测流
	dir := paths.BackupDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	want := "-- MySQL dump\n-- Dump completed on 2026-09-25\nCREATE TABLE parts (id INT);\n"
	if err := os.WriteFile(filepath.Join(dir, "backup_20260925_101530.sql"), []byte(want), 0o600); err != nil {
		t.Fatal(err)
	}

	w := doJSON(t, s, "POST", "/api/backup", tok, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("备份 = %d, body=%s", w.Code, w.Body.String())
	}
	if got := w.Body.String(); got != want {
		t.Errorf("响应体不是备份内容：\n得到 %q\n期望 %q", got, want)
	}
	if cd := w.Header().Get("Content-Disposition"); !strings.Contains(cd, "attachment") ||
		!strings.Contains(cd, "backup_20260925_101530.sql") {
		t.Errorf("Content-Disposition = %q，应含 attachment 与文件名", cd)
	}
	// 响应里不得出现备份目录的绝对路径
	if strings.Contains(w.Body.String(), dir) {
		t.Error("响应里出现了服务器备份目录路径")
	}
}

// 备份文件列表只给文件名，不给路径。
func TestListBackupsReturnsNamesOnly(t *testing.T) {
	apps := &backupApps{}
	s := newBackupServer(t, apps)
	tok := issueAdmin(t, s)

	dir := paths.BackupDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"backup_20260925_101530.sql", "pre_restore_20260929_110000.db", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("-- Dump completed\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	w := doJSON(t, s, "GET", "/api/backup/files", tok, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("列备份 = %d, body=%s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), dir) {
		t.Errorf("列表响应泄露了服务器目录：%s", w.Body.String())
	}
	var out struct {
		Files []backupEntry `json:"files"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, f := range out.Files {
		names[f.Name] = true
	}
	if !names["backup_20260925_101530.sql"] || !names["pre_restore_20260929_110000.db"] {
		t.Errorf("列表缺少备份文件，实际：%v", names)
	}
	if names["notes.txt"] {
		t.Error("列表混入了非备份文件（.txt）——它不是合法的恢复源")
	}
}

// 空目录返回空列表而不是 500：首启后第一次打开备份屏不该看到报错。
func TestListBackupsOnMissingDirReturnsEmpty(t *testing.T) {
	apps := &backupApps{}
	s := newBackupServer(t, apps)
	tok := issueAdmin(t, s)
	if err := os.RemoveAll(paths.BackupDir()); err != nil {
		t.Fatal(err)
	}
	w := doJSON(t, s, "GET", "/api/backup/files", tok, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("无备份目录时 = %d，期望 200 + 空列表", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"files":[]`) && !strings.Contains(w.Body.String(), `"files": []`) {
		t.Errorf("响应 = %s，期望空列表", w.Body.String())
	}
}
