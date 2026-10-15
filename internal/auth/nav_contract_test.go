package auth

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"testing"
)

// 前端导航与权限模块清单的**跨语言契约**。
//
// 两边是同一个信息的两份表现：Go 侧是权限表，TS 侧是侧栏 nav.ts。它们必须一致，
// 而 TypeScript 拦不住这种漂移——nav.ts 的 ModuleKey 是手写的联合类型，后端新增
// 一个模块时 TS 编译依然通过，只是那个模块**永远不显示在侧栏上**，也不会有任何
// 报错。这类"加了功能但没有入口"的问题只能靠对着两份清单断言来发现。
//
// 这里直接读 nav.ts 的源码文本。不做完整的 TS 解析——只需要 `{ key: 'x'` 这个
// 稳定形状。为了防止正则失配后"提取到空集"导致测试假通过，下面同时断言
// 提取结果非空且与期望集合完全相等。
const navPath = "../../web/src/lib/nav.ts"

var navKeyRe = regexp.MustCompile(`key:\s*'([a-z_]+)'`)

func readNavKeys(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(navPath))
	if err != nil {
		t.Fatalf("读取 %s 失败：%v（此测试依赖仓库布局，路径要跟着文件走）", navPath, err)
	}
	seen := map[string]bool{}
	var out []string
	for _, m := range navKeyRe.FindAllStringSubmatch(string(b), -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			out = append(out, m[1])
		}
	}
	if len(out) == 0 {
		t.Fatalf("从 %s 里一个模块键都没提取到——正则失配，测试会假通过", navPath)
	}
	sort.Strings(out)
	return out
}

// nonNavModules 是「有权限模块、但侧栏里没有对应项」的模块白名单。
//
// 2026-10-15 删掉 system（应用维护：检查/下载/替换 exe，曾仅管理员可写）之后
// 它是空的——更新流程对齐 Fyne 旧版之后，那三项能力不再是一个需要权限档位的
// 独立模块，于是也不需要"占侧栏一项但不进导航"的例外。
//
// 将来若再引入只在别处出现（例如仅在「设置」页签里）的模块，必须在这里显式登记
// 并写明理由：TestNonNavModulesAreDeliberate 要求实际集合与本白名单完全一致，
// 多一个少一个都失败。
func nonNavModules() map[string]bool {
	return map[string]bool{}
}

// 导航必须是权限模块清单去掉「非导航模块」后的样子。
//
// 反过来也成立：后端新增模块而前端没加导航 → 这里失败。
func TestNavMatchesPermissionModules(t *testing.T) {
	nonNav := nonNavModules()

	var want []string
	for _, m := range AllModules() {
		if !nonNav[m] {
			want = append(want, m)
		}
	}
	sort.Strings(want)

	got := readNavKeys(t)

	if len(got) != len(want) {
		t.Errorf("导航项数 = %d，权限模块（去掉 %v）= %d\ngot:  %v\nwant: %v",
			len(got), keysOf(nonNav), len(want), got, want)
	}
	i, j := 0, 0
	for i < len(got) && j < len(want) {
		switch {
		case got[i] == want[j]:
			i++
			j++
		case got[i] < want[j]:
			t.Errorf("导航里有 %q，权限模块清单里没有——它对应的接口不会被授权", got[i])
			i++
		default:
			t.Errorf("权限模块 %q 没有导航入口——用户看不到它", want[j])
			j++
		}
	}
	for ; i < len(got); i++ {
		t.Errorf("导航里有 %q，权限模块清单里没有", got[i])
	}
	for ; j < len(want); j++ {
		t.Errorf("权限模块 %q 没有导航入口", want[j])
	}
}

// 「非导航模块」集合必须与 TestNavMatchesPermissionModules 里的白名单**完全一致**：
// 多一个（悄悄长胖）或少一个（白名单里留了个不存在的模块）都失败。
//
// 目前白名单是空的——2026-10-15 删掉 system 之后，每个权限模块都有侧栏入口。
func TestNonNavModulesAreDeliberate(t *testing.T) {
	all := map[string]bool{}
	for _, m := range AllModules() {
		all[m] = true
	}
	nav := map[string]bool{}
	for _, k := range readNavKeys(t) {
		nav[k] = true
	}
	var missing []string
	for m := range all {
		if !nav[m] {
			missing = append(missing, m)
		}
	}
	sort.Strings(missing)

	want := keysOf(nonNavModules())
	sort.Strings(want)
	if len(missing) != len(want) {
		t.Errorf("非导航模块 = %v，白名单 = %v。新增非导航模块需要在本文件的 nonNav 白名单里显式承认。", missing, want)
		return
	}
	for i := range want {
		if missing[i] != want[i] {
			t.Errorf("非导航模块 = %v，白名单 = %v（不一致）", missing, want)
			return
		}
	}
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
