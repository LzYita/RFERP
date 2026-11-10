package update

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

// writeUpdateZip 按给定顺序写 zip：顺序是断言的一部分（旧实现取"第一个 .exe"）。
func writeUpdateZip(t *testing.T, entries []struct{ name, body string }) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "RFERP-update.zip")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for _, e := range entries {
		w, err := zw.Create(e.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(e.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return p
}

// 包里混入别的 exe 时，必须按名字取 RFERP.exe，而不是"第一个 .exe"。
func TestExtractExePicksRFERPByName(t *testing.T) {
	src := writeUpdateZip(t, []struct{ name, body string }{
		{"server.exe", "wrong binary"},
		{"RFERP.exe", "right binary"},
	})
	dst := filepath.Join(t.TempDir(), "RFERP.new.exe")
	if err := extractExe(src, dst); err != nil {
		t.Fatalf("extractExe: %v", err)
	}
	body, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "right binary" {
		t.Fatalf("取到的是 %q，期望 RFERP.exe 的内容", body)
	}
}

// 包内路径带目录时按最后一段匹配（历史包是平铺的，但不该假设）。
func TestExtractExeAcceptsNestedRFERP(t *testing.T) {
	src := writeUpdateZip(t, []struct{ name, body string }{
		{"dist/RFERP.exe", "nested binary"},
	})
	dst := filepath.Join(t.TempDir(), "RFERP.new.exe")
	if err := extractExe(src, dst); err != nil {
		t.Fatalf("extractExe: %v", err)
	}
	body, _ := os.ReadFile(dst)
	if string(body) != "nested binary" {
		t.Fatalf("内容 = %q", body)
	}
}

// 没有 RFERP.exe 的包必须拒绝，而不是随便装一个 exe 当新版本。
func TestExtractExeRejectsPackageWithoutRFERP(t *testing.T) {
	src := writeUpdateZip(t, []struct{ name, body string }{
		{"server.exe", "wrong binary"},
	})
	err := extractExe(src, filepath.Join(t.TempDir(), "RFERP.new.exe"))
	if err == nil {
		t.Fatal("没有 RFERP.exe 的更新包被接受了")
	}
}
