//go:build windows

package mysqlfind

import "testing"

// FindMysqldump 的契约：返回非空路径时，它必须真的能跑。
//
// 本机没装 MySQL 的机器上跳过（CI 的 Windows runner 就是这种）；
// 开发机上有 MySQL 时会实际跑一次 --version，等于顺手验证了发现链路。
func TestFindMysqldumpReturnsRunnablePath(t *testing.T) {
	p, tried := FindMysqldump("", "")
	if p == "" {
		t.Skipf("本机没有 mysqldump；已尝试：%v", tried)
	}
	if !dumpWorks(p) {
		t.Fatalf("FindMysqldump 返回了 %q，但它跑不起来", p)
	}
}
