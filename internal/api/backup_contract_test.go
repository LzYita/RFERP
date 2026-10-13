package api

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// 备份文件名的**跨语言契约**。
//
// 可恢复文件名既是一道门（导不出时间戳的文件不许恢复），又是确认口令的来源，
// 所以它必须同时存在于两端：服务端 internal/api/backup.go 的 restoreFilePattern
// 决定"这个文件能不能恢复"，前端 web/src/lib/backup.ts 的 restoreTokenFor 决定
// "下拉框里给不给你令牌"。
//
// 这类漂移编译器拦不住：TS 那边改窄了正则，TS 照样编译通过，只是用户的旧备份
// 在界面里变成"无法识别时间戳"——一个既不报错、也看不出原因的功能缺失。
// 这次就是这么发现的：D:\仓库数据 里的 backup_20260924_170830.final.sql
// 恢复不了，而软件只回一句「请选择本应用生成的文件」。
//
// 做法：直接读 TS 源码里的正则文本，与 Go 侧对同一批**真实文件名**的判定逐一比对。
// 不做完整 TS 解析——只需要那个字面量正则。

const backupTsPath = "../../web/src/lib/backup.ts"

// tsRegexLiteralFromTS 从 TS 源码里抠出 restoreTokenFor 用的那条正则字面量文本。
//
// 它锁定在函数体内 `= /…/` 那一处，避免误取文件里其它正则。不做完整 TS 解析。
func tsRegexLiteralFromTS(t *testing.T) string {
	t.Helper()
	src := mustRead(t, backupTsPath)
	// 从 `export function restoreTokenFor` 之后开始找第一个正则字面量。
	i := strings.Index(src, "export function restoreTokenFor")
	if i < 0 {
		t.Fatal("TS 源码里找不到 restoreTokenFor —— 函数被改名或挪走，提取失效")
	}
	body := src[i:]
	s := strings.Index(body, "= /")
	if s < 0 {
		t.Fatal("restoreTokenFor 函数体内找不到正则字面量 `= /` —— 写法变了，提取失效")
	}
	body = body[s+3:]
	e := strings.Index(body, "/.exec")
	if e < 0 {
		t.Fatal("正则字面量后面找不到 /.exec —— 写法变了，提取失效")
	}
	return body[:e]
}

// tsRegexMatches 用**真的 Node** 求值 TS 那条正则，判断它是否匹配文件名。
//
// 故意不在 Go 里复刻一遍规则：复刻等于又写一份，漂移了就等于没测。直接把抠出来的
// 正则原样交给 Node，测的才是用户实际跑的那条规则。
func tsRegexMatches(t *testing.T, re, name string) bool {
	t.Helper()
	// 正则原样交给 Node：既避开 Go 与 JS 的转义差异，也让测试跑的确实是
	// 用户实际在跑的那条规则，而不是我们在 Go 里复刻的一份。
	const script = `const re = new RegExp(process.argv[1]); process.stdout.write(re.test(process.argv[2]) ? '1' : '0')`
	out, err := exec.Command("node", "-e", script, re, name).Output()
	if err != nil {
		t.Fatalf("用 Node 求值 TS 正则失败：%v", err)
	}
	return strings.TrimSpace(string(out)) == "1"
}

// TestBackupFilenameContract 用真实文件名锁住两端一致。
//
// 语料来自 D:\仓库数据 的实际内容（含 .final / .upgraded 两个本程序不产出的
// 变体），外加一批必须被拒的反例——反例尤其重要：只测"该过的能过"的话，
// 两端一起放宽到 `.*\.sql` 也会一起通过。
func TestBackupFilenameContract(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skipf("找不到 node，无法求值 TS 正则：%v", err)
	}
	tsRe := tsRegexLiteralFromTS(t)
	if tsRe == "" {
		t.Fatal("没提取到 TS 正则——提取方式失效，测试会假通过")
	}

	cases := []struct {
		name string
		ok   bool
	}{
		// —— D:\仓库数据 里的真实文件 ——
		{"backup_20260721_154617.sql", true},
		{"backup_20260920_141609.sql", true},
		{"backup_20260924_170830.sql", true},
		{"backup_20260924_170830.final.sql", true},
		{"backup_20260924_170830.upgraded.sql", true},
		{"pre_restore_20260924_172919.sql", true},
		{"pre_clear_20260924_105500.sql", true},

		// —— 反例：结尾不是 .sql/.db ——
		{"backup_20260925_101530.sql.bak", false},
		{"backup_20260925_101530.zip", false},
		{"audit_log_20260721_20260721.csv", false},
		{"batch_trace.csv", false},
		{"rferp.log", false},
		{"backup_20260925_101530", false},

		// —— 反例：前缀或时间戳不对 ——
		{"backup.sql", false},
		{"backup_手动拷贝.sql", false},
		{"backup_20260925.sql", false},
		{"backup_20260925_1015.sql", false},
		{"random_20260925_101530.sql", false},
		{"20260925_101530.sql", false},
		{"", false},

		// —— 反例：qualifier 只允许一个点分段，且字符集有限 ——
		{"backup_20260925_101530.a.b.sql", false},
		{"backup_20260925_101530.a b.sql", false},
		{"backup_20260925_101530..sql", false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, goOK := BackupTimestamp(c.name)

			tsOK := tsRegexMatches(t, tsRe, c.name)

			if goOK != c.ok {
				t.Errorf("Go 侧判定 = %v，期望 %v（文件名 %q）", goOK, c.ok, c.name)
			}
			if tsOK != goOK {
				t.Errorf("两端不一致：Go=%v TS=%v（文件名 %q）", goOK, tsOK, c.name)
			}
		})
	}
}

// TestBackupTimestampValue 确认取出的时间戳是对的，而不只是"匹配了"。
//
// 同一时刻存在三个世代（.sql / .final.sql / .upgraded.sql），它们必须导出**同一个**
// 时间戳——口令是"你读过后果"的确认，不是文件版本的选择器。
func TestBackupTimestampValue(t *testing.T) {
	for _, n := range []string{
		"backup_20260924_170830.sql",
		"backup_20260924_170830.final.sql",
		"backup_20260924_170830.upgraded.sql",
	} {
		ts, ok := BackupTimestamp(n)
		if !ok {
			t.Errorf("%q 不该被拒", n)
			continue
		}
		if ts != "20260924_170830" {
			t.Errorf("%q 导出时间戳 %q，期望 20260924_170830", n, ts)
		}
	}
	// 首尾空白不该影响识别——文件列表来自 JSON，客户端可能带上空白。
	if ts, ok := BackupTimestamp("  backup_20260924_170830.final.sql  "); !ok || ts != "20260924_170830" {
		t.Errorf("带空白的文件名：ok=%v ts=%q", ok, ts)
	}
}

func mustRead(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(p))
	if err != nil {
		t.Fatalf("读取 %s 失败：%v", p, err)
	}
	return string(b)
}
