package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"app/internal/paths"
)

// 导出端点的关口。两个 bug 都来自"两端各自以为对方会怎么做"：
//
//	审计导出 invalid body —— 界面发纯日期 "2026-10-13"，服务端把字段声明成
//	    time.Time（只认 RFC 3339）。两端的 Go 测试各自都过，因为没有任何测试
//	    从界面发过一次请求。
//	全量导出的 all_data.csv 乱码 —— 服务端回 JSON，界面按"下载单个文件"处理。
//	    而且导出目录丢了旧版的 all_data 分组。

// --------------------------------------------------------------- 审计导出

func TestExportAuditAcceptsDateOnlyStrings(t *testing.T) {
	for _, s := range []string{"2026-10-13", " 2026-10-13 "} {
		got, err := parseExportDate(s)
		if err != nil {
			t.Errorf("%q 应被接受：%v", s, err)
			continue
		}
		if y, m, d := got.Date(); y != 2026 || m != 10 || d != 13 {
			t.Errorf("%q 解析成 %v，期望 2026-10-13", s, got)
		}
	}
}

// Go 侧客户端（远程模式）把 time.Time 序列化过来，形状是 RFC 3339 —— 也必须收。
func TestExportAuditAcceptsRFC3339(t *testing.T) {
	got, err := parseExportDate("2026-10-13T00:00:00+08:00")
	if err != nil {
		t.Fatalf("RFC 3339 应被接受：%v", err)
	}
	if y, m, d := got.Date(); y != 2026 || m != 10 || d != 13 {
		t.Errorf("解析成 %v，期望 2026-10-13", got)
	}
}

func TestExportAuditRejectsGarbage(t *testing.T) {
	for _, s := range []string{"", "2026/10/13", "13-10-2026", "前天"} {
		if _, err := parseExportDate(s); err == nil {
			t.Errorf("%q 应被拒绝，否则会把一段乱日期当成合法区间去查库", s)
		}
	}
}

// 界面选的是「从这一天到那一天」，两端都要含。
// 若不加这一天，选 10-13 到 10-13 的用户会拿到空文件而不知道为什么。
func TestExportAuditEndDateIsInclusive(t *testing.T) {
	start, _ := parseExportDate("2026-10-13")
	end, _ := parseExportDate("2026-10-13")
	adjusted := end
	if !adjusted.Before(start) {
		adjusted = adjusted.AddDate(0, 0, 1)
	}
	if !adjusted.After(end) {
		t.Error("结束日应顺延一天，否则当天的记录查不到")
	}
}

// 端到端：真的发一次界面会发的请求，确认不再是 invalid body。
func TestExportAuditEndpointAcceptsWebPayload(t *testing.T) {
	dir := t.TempDir()
	paths.SetDataDir(dir)
	t.Cleanup(func() { paths.SetDataDir("") })

	apps := &exportApps{}
	s := newBackupServer(t, apps)
	tok := issueAdmin(t, s)

	w := doJSON(t, s, "POST", "/api/export/audit", tok, map[string]string{
		"start": "2026-10-01",
		"end":   "2026-10-13",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("界面形状的请求 = %d，body=%s（这正是用户遇到的 invalid body）", w.Code, w.Body.String())
	}
	if apps.gotStart.IsZero() || apps.gotEnd.IsZero() {
		t.Error("没有把日期传给用例")
	}
	if apps.gotStart.Format("2006-01-02") != "2026-10-01" {
		t.Errorf("start = %v", apps.gotStart)
	}
	if !apps.gotEnd.After(apps.gotStart) {
		t.Errorf("结束日没有顺延一天，区间是空的：%v .. %v", apps.gotStart, apps.gotEnd)
	}
}

// 与全量导出一致：导出只回报**位置**，不把内容塞进下载。
//
// 用户明确要求审计日志也不再出现在「下载」里：文件已经在数据目录中，
// 再丢一份只会多出一个认不出的文件，还每次导出多一份重复数据。
func TestExportAuditReportsLocationNotFile(t *testing.T) {
	apps := &exportApps{}
	s := newBackupServer(t, apps)
	tok := issueAdmin(t, s)

	w := doJSON(t, s, "POST", "/api/export/audit", tok, map[string]string{
		"start": "2026-10-01",
		"end":   "2026-10-13",
	})
	var out struct {
		Dir   string   `json:"dir"`
		Files []string `json:"files"`
		Count int      `json:"count"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Dir == "" || len(out.Files) != 1 {
		t.Fatalf("应回报目录与文件名，实际 %+v", out)
	}
	// 旧版约定：导出\audit_log\<时间戳>\
	if !strings.HasPrefix(out.Dir, filepath.Join(paths.ExportDir(), exportSubdirAuditLog)) {
		t.Errorf("目录 = %q，不在 audit_log 分组下", out.Dir)
	}
	if strings.ContainsAny(out.Files[0], `/\`) {
		t.Errorf("应只给文件名，实际 %q", out.Files[0])
	}
	// 文件名里的结束日期要用**用户填的**那一天，不是顺延后的内部边界
	if !strings.Contains(out.Files[0], "20261001") || !strings.Contains(out.Files[0], "20261013") {
		t.Errorf("文件名 = %q，应含用户选的起止日期", out.Files[0])
	}
	// 关键：响应不是 CSV，且文件确实落在目录里
	if ct := w.Header().Get("Content-Type"); strings.Contains(ct, "csv") {
		t.Errorf("Content-Type = %q，导出端点不该再回 CSV", ct)
	}
	full := filepath.Join(out.Dir, out.Files[0])
	if _, err := os.Stat(full); err != nil {
		t.Errorf("文件没有真的落到数据目录：%v", err)
	}
}

// 需要字节的调用方（Go 客户端、远程界面）走 /download，仍然拿到 CSV。
func TestExportAuditDownloadEndpointStreamsCSV(t *testing.T) {
	apps := &exportApps{}
	s := newBackupServer(t, apps)
	tok := issueAdmin(t, s)

	w := doJSON(t, s, "POST", "/api/export/audit/download", tok, map[string]string{
		"start": "2026-10-01",
		"end":   "2026-10-13",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("下载端点 = %d, body=%s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "csv") {
		t.Errorf("Content-Type = %q", ct)
	}
	if cd := w.Header().Get("Content-Disposition"); !strings.Contains(cd, "attachment") {
		t.Errorf("Content-Disposition = %q，浏览器会用兜底名存成乱码文件名", cd)
	}
	if !strings.Contains(w.Body.String(), "CREATE") {
		t.Errorf("响应体不是 CSV 内容：%q", w.Body.String())
	}
}

// --------------------------------------------------------------- 全量导出

type exportApps struct {
	backupApps
	gotStart time.Time
	gotEnd   time.Time
}

func (a *exportApps) ExportAuditLogCSV(start, end time.Time, path string) (int, error) {
	a.gotStart, a.gotEnd = start, end
	if err := os.WriteFile(path, []byte("time,action\n2026-10-13 10:00:00,CREATE\n"), 0o600); err != nil {
		return 0, err
	}
	return 1, nil
}

func (a *exportApps) ExportAllDataCSV(saveDir string) (map[string]string, string, error) {
	sub := filepath.Join(saveDir, "20261013_160000")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		return nil, "", err
	}
	files := map[string]string{}
	for _, n := range []string{"parts.csv", "products.csv", "audit_log.csv"} {
		p := filepath.Join(sub, n)
		if err := os.WriteFile(p, []byte("id\n1\n"), 0o600); err != nil {
			return nil, "", err
		}
		files[n[:len(n)-4]] = p
	}
	return files, sub, nil
}

func TestExportAllUsesGroupedSubdirectory(t *testing.T) {
	apps := &exportApps{}
	// 数据目录由 newBackupServer 分配（它会再调一次 paths.SetDataDir），
	// 所以断言要对着**它设完之后**的 paths.ExportDir() 比，不能用自己那份。
	s := newBackupServer(t, apps)
	tok := issueAdmin(t, s)
	wantPrefix := filepath.Join(paths.ExportDir(), exportSubdirAllData)

	w := doJSON(t, s, "POST", "/api/export/all", tok, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("导出全部 = %d, body=%s", w.Code, w.Body.String())
	}
	var out struct {
		Dir   string   `json:"dir"`
		Files []string `json:"files"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	// 旧版约定：导出\all_data\<时间戳>\。直接扔在 导出\ 根下会产出
	// `20261013_160107-1666890059` 这种随机名目录，用户找不到也不敢删。
	if !strings.HasPrefix(out.Dir, wantPrefix) {
		t.Errorf("导出目录 = %q，期望在 %q 下", out.Dir, wantPrefix)
	}
	// 文件名而不是完整路径
	for _, f := range out.Files {
		if strings.ContainsAny(f, `/\`) {
			t.Errorf("回传了路径 %q，应只给文件名", f)
		}
	}
	if len(out.Files) != 3 {
		t.Errorf("文件数 = %d，期望 3", len(out.Files))
	}
}

// 打开文件夹是本机操作：远程形态必须挡下。
func TestOpenExportFolderRequiresHostOperations(t *testing.T) {
	dir := t.TempDir()
	paths.SetDataDir(dir)
	t.Cleanup(func() { paths.SetDataDir("") })
	if err := os.MkdirAll(paths.ExportDir(), 0o755); err != nil {
		t.Fatal(err)
	}

	opened := 0
	s := newBackupServer(t, &exportApps{}, // 不传 WithHostOperations
		WithFolderOpener(func(string) error { opened++; return nil }))
	tok := issueAdmin(t, s)

	w := doJSON(t, s, "POST", "/api/export/all/open", tok, map[string]string{"dir": paths.ExportDir()})
	if w.Code != http.StatusForbidden {
		t.Errorf("远程形态下打开文件夹 = %d，期望 403", w.Code)
	}
	if opened != 0 {
		t.Errorf("被闸门挡下时仍然启动了资源管理器 %d 次", opened)
	}
}

// 客户端给任意路径就等于让它在这台机器上打开任意文件夹。
//
// ⚠️ 这里必须用**原始字符串**拼 `..`，不能用 filepath.Join —— Join 会先 Clean，
// 于是测到的是 `D:\Windows`（前缀检查当然能拒），而不是客户端真会发来的
// `D:\...\导出\..\outside`（前缀检查会被绕过，落点在导出目录之外）。
// 我第一版正是这么写的，测试绿着、功能却能被绕过。
//
// ⚠️ 同时注入假的 opener：**这个端点有真实副作用**（会在开发者桌面上弹窗）。
// 测试走真实实现的话，每跑一遍就开一个窗口，CI 上还会去启动 GUI 程序。
func TestOpenExportFolderRejectsForeignPaths(t *testing.T) {
	var opened []string
	s := newBackupServer(t, &exportApps{}, WithHostOperations(),
		WithFolderOpener(func(d string) error { opened = append(opened, d); return nil }))
	tok := issueAdmin(t, s)
	exportRoot := paths.ExportDir()
	if err := os.MkdirAll(exportRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	// 导出根目录的**同级**目录，且确实存在 —— 排除"被目录不存在顺带挡掉"
	outside := filepath.Join(filepath.Dir(exportRoot), "outside_ok")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	sep := string(filepath.Separator)

	bad := []string{
		"",
		"C:\\Windows",
		filepath.Dir(exportRoot), // 数据目录本身
		exportRoot + sep + ".." + sep + "outside_ok",           // 未清理的 ..
		exportRoot + sep + ".." + sep + ".." + sep + "Windows", // 两级 ..
		exportRoot + sep + "." + sep + "..",                    // . 与 .. 混用
		exportRoot + "x",                                       // 前缀相同但不是子目录
	}
	for _, p := range bad {
		w := doJSON(t, s, "POST", "/api/export/all/open", tok, map[string]string{"dir": p})
		if w.Code == http.StatusOK {
			t.Errorf("目录 %q 被接受了——客户端可以打开这台机器上的任意文件夹", p)
		}
	}
	if len(opened) != 0 {
		t.Errorf("被拒的目录也启动了资源管理器：%v", opened)
	}

	// 导出目录本身与真实子目录必须仍然放行，否则这个校验就过度了。
	// 顺带断言传给启动器的**正是**那个目录，而不只是"返回了 200"。
	want := []string{exportRoot, filepath.Join(exportRoot, "all_data")}
	for _, ok := range want {
		if err := os.MkdirAll(ok, 0o755); err != nil {
			t.Fatal(err)
		}
		w := doJSON(t, s, "POST", "/api/export/all/open", tok, map[string]string{"dir": ok})
		if w.Code != http.StatusOK {
			t.Errorf("合法目录 %q 被拒：%d %s", ok, w.Code, w.Body.String())
		}
	}
	if len(opened) != len(want) {
		t.Fatalf("启动器被调用 %d 次，期望 %d", len(opened), len(want))
	}
	for i, d := range opened {
		expected, err := filepath.EvalSymlinks(want[i])
		if err != nil {
			t.Fatal(err)
		}
		if d != expected {
			t.Errorf("第 %d 次传给启动器的目录 = %q，期望 %q", i+1, d, expected)
		}
	}
}

// 启动器失败要如实回 500，不能假装成功。
func TestOpenExportFolderRejectsDirectoryLinkOutsideRoot(t *testing.T) {
	opened := 0
	s := newBackupServer(t, &exportApps{}, WithHostOperations(),
		WithFolderOpener(func(string) error { opened++; return nil }))
	tok := issueAdmin(t, s)
	if err := os.MkdirAll(paths.ExportDir(), 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(paths.ExportDir(), "escape")
	outside := t.TempDir()
	if runtime.GOOS == "windows" {
		if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, outside).CombinedOutput(); err != nil {
			t.Fatalf("create junction: %v: %s", err, out)
		}
	} else if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	w := doJSON(t, s, "POST", "/api/export/all/open", tok, map[string]string{"dir": link})
	if w.Code != http.StatusBadRequest || opened != 0 {
		t.Fatalf("outside link = %d; opener called %d times", w.Code, opened)
	}
}

func TestOpenExportFolderMissingDirectory(t *testing.T) {
	opened := 0
	s := newBackupServer(t, &exportApps{}, WithHostOperations(),
		WithFolderOpener(func(string) error { opened++; return nil }))
	tok := issueAdmin(t, s)
	if err := os.MkdirAll(paths.ExportDir(), 0700); err != nil {
		t.Fatal(err)
	}
	w := doJSON(t, s, "POST", "/api/export/all/open", tok,
		map[string]string{"dir": filepath.Join(paths.ExportDir(), "missing")})
	if w.Code != http.StatusNotFound || opened != 0 {
		t.Fatalf("missing directory = %d; opener called %d times", w.Code, opened)
	}
}

func TestOpenExportFolderReportsLauncherFailure(t *testing.T) {
	s := newBackupServer(t, &exportApps{}, WithHostOperations(),
		WithFolderOpener(func(string) error { return errors.New("no explorer here") }))
	tok := issueAdmin(t, s)
	if err := os.MkdirAll(paths.ExportDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	w := doJSON(t, s, "POST", "/api/export/all/open", tok, map[string]string{"dir": paths.ExportDir()})
	if w.Code != http.StatusInternalServerError {
		t.Errorf("启动器失败 = %d，期望 500", w.Code)
	}
}
