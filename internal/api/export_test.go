package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
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
	// 下载到的必须真的是 CSV，而不是一段 JSON
	if ct := w.Header().Get("Content-Disposition"); ct == "" {
		t.Error("没有 Content-Disposition，浏览器会用兜底名存成乱码文件名")
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

	s := newBackupServer(t, &exportApps{}) // 不传 WithHostOperations
	tok := issueAdmin(t, s)

	w := doJSON(t, s, "POST", "/api/export/all/open", tok, map[string]string{"dir": paths.ExportDir()})
	if w.Code != http.StatusForbidden {
		t.Errorf("远程形态下打开文件夹 = %d，期望 403", w.Code)
	}
}

// 客户端给任意路径就等于让它在这台机器上打开任意文件夹。
func TestOpenExportFolderRejectsForeignPaths(t *testing.T) {
	dir := t.TempDir()
	paths.SetDataDir(dir)
	t.Cleanup(func() { paths.SetDataDir("") })
	if err := os.MkdirAll(paths.ExportDir(), 0o755); err != nil {
		t.Fatal(err)
	}

	s := newBackupServer(t, &exportApps{}, WithHostOperations())
	tok := issueAdmin(t, s)

	for _, bad := range []string{
		"",
		"C:\\Windows",
		dir,
		filepath.Join(dir, "导出", "..", "..", "Windows"),
	} {
		w := doJSON(t, s, "POST", "/api/export/all/open", tok, map[string]string{"dir": bad})
		if w.Code == http.StatusOK {
			t.Errorf("目录 %q 被接受了——客户端可以打开这台机器上的任意文件夹", bad)
		}
	}
}
