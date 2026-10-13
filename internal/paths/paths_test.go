package paths

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// ResolveUnder / Under 的关口 —— 这是「在配置根目录下拼子路径」的唯一判据。
//
// 收在一处的价值就在于：判据只有一份，于是可以把它测穷。分散在各包时，
// 每处都只被间接覆盖，而漏掉一种形态（比如 `..` 恰好能过 Base 检查）不会被发现。
//
// 起因是 CodeQL 报了 30 条 go/path-injection。逐条追下来 **0 个真实漏洞**，
// 但过程中发现同一件事在三个地方各写了一遍（backuplayout.Resolve 的
// Base+Rel、api 的 dirUnder、以及各处裸的 filepath.Join），且其中一处
// （dirUnder 的字符串前缀）被 `..` 绕过过。现在只有这一份。

// ResolveUnder 必须**接受**的：单一文件名。
func TestResolveUnderAcceptsPlainNames(t *testing.T) {
	root := filepath.Join("D:", "仓库数据", "备份")
	for _, name := range []string{
		"backup_20260924_170830.sql",
		"pre_restore_20260924_172919.utf8.sql",
		"rferp.log",
		"parts.csv",
		"auto_20261013_160000.db",
	} {
		got, err := ResolveUnder(root, name)
		if err != nil {
			t.Errorf("%q 应被接受：%v", name, err)
			continue
		}
		if filepath.Dir(got) != root {
			t.Errorf("%q 解析到 %q，不在 %q 下", name, got, root)
		}
		if filepath.Base(got) != name {
			t.Errorf("%q 解析成 %q，文件名被改写", name, got)
		}
	}
}

// 必须**拒绝**的：所有能逃出 root 或不该被当作文件名的形态。
func TestResolveUnderRejectsEscapesAndPaths(t *testing.T) {
	root := filepath.Join("D:", "仓库数据", "备份")
	cases := []struct{ name, why string }{
		{"", "空"},
		{"..", "逃逸（注意 filepath.Base(\"..\")==\"..\"，只靠 Base 检查拦不住）"},
		{"../secret.sql", "上跳一级"},
		{"..\\..\\secret.sql", "上跳两级"},
		{"a/../../x.sql", "先下后上"},
		{"/etc/passwd", "绝对路径（Unix）"},
		{"C:\\Windows\\System32\\config\\SAM", "绝对路径（Windows）"},
		{"\\\\server\\share\\x.sql", "UNC 路径"},
		{"sub\\backup_20260924_170830.sql", "嵌套相对路径：不逃逸，但客户端不该能指到子目录"},
		{"sub/x.sql", "同上，正斜杠"},
		{"manual/backup_x.sql", "同上，真实存在的分类子目录也不许"},
		{"NUL", "Windows 保留设备名"},
		{"CON", "Windows 保留设备名"},
	}
	for _, c := range cases {
		if _, err := ResolveUnder(root, c.name); err == nil {
			t.Errorf("%q 被接受了（%s）", c.name, c.why)
		}
	}
}

// root 本身是什么都不影响判据：判据只看 name 能不能安全拼进去。
// 这一条很要紧 —— root 来自用户配置（首启向导选的目录），本应用的设计就是让用户选。
func TestResolveUnderDoesNotJudgeRoot(t *testing.T) {
	for _, root := range []string{`D:\仓库数据\备份`, `/home/u/data`, `relative\root`} {
		if _, err := ResolveUnder(root, "x.sql"); err != nil {
			t.Errorf("root=%q 时普通文件名不该被拒：%v", root, err)
		}
		if _, err := ResolveUnder(root, ".."); err == nil {
			t.Errorf("root=%q 时 .. 不该被接受", root)
		}
	}
}

// Under：判断一个**已经拼好的路径**在不在根目录内。
func TestUnder(t *testing.T) {
	root := filepath.Join("D:", "仓库数据", "导出")
	sep := string(filepath.Separator)

	yes := []string{
		root,
		filepath.Join(root, "all_data"),
		filepath.Join(root, "all_data", "20261013_160000"),
		root + sep + ".." + sep + "导出", // Clean 之后就是 root
	}
	for _, p := range yes {
		if !Under(root, p) {
			t.Errorf("Under(%q, %q) = false，期望 true", root, p)
		}
	}

	no := []string{
		"",
		root + "x",                          // 前缀相同但不是子目录 —— 字符串前缀比会误判为真
		root + sep + ".." + sep + "outside", // 未清理的 ..（客户端真会发来的形状）
		filepath.Dir(root),                  // 父目录
		`C:\Windows`,
		`/etc`,
	}
	for _, p := range no {
		if Under(root, p) {
			t.Errorf("Under(%q, %q) = true，期望 false", root, p)
		}
	}
}

// Under 与 ResolveUnder 的关系：经 ResolveUnder 拼出来的路径，Under 必须认得。
// 两者都用来防"越出根目录"，判据若不一致就会出现"一处放行、一处拦下"。
func TestUnderAgreesWithResolveUnder(t *testing.T) {
	root := filepath.Join("D:", "仓库数据", "备份")
	for _, name := range []string{"x.sql", "rferp.log"} {
		full, err := ResolveUnder(root, name)
		if err != nil {
			t.Fatalf("ResolveUnder(%q): %v", name, err)
		}
		if !Under(root, full) {
			t.Errorf("ResolveUnder 拼出的 %q 竟然不被 Under 认可", full)
		}
	}
}

// 平台差异要显式记下来：Windows 上反斜杠是分隔符，判定会更严。
func TestResolveUnderPlatformNote(t *testing.T) {
	root := t.TempDir()
	if _, err := ResolveUnder(root, "a/b.sql"); err == nil {
		t.Error("含分隔符的名字在任何平台都应被拒")
	}
	if runtime.GOOS == "windows" {
		if _, err := ResolveUnder(root, "a\\b.sql"); err == nil {
			t.Error("Windows 上反斜杠也是分隔符，应被拒")
		}
	}
	if !strings.Contains(ErrNameNotLocal.Error(), "..") {
		t.Error("错误信息应点明 .. 这类形态，便于排查")
	}
}
