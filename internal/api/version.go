package api

import (
	"strconv"
	"strings"
)

// 版本比较（FR-15）。
//
// 只为"服务端要不要拦住这台客户端"服务，因此故意做得**宽松**：
// 解析不清的版本一律当作"不低"，宁可放过一个旧版本，也不要把一个版本号写法
// 奇特的正常客户端永久挡在门外——后者会让客户连日志都拿不到。

// CompareVersions 比较两个版本号：a<b 返回 -1，相等 0，a>b 返回 1。
func CompareVersions(a, b string) int {
	as, bs := versionParts(a), versionParts(b)
	n := len(as)
	if len(bs) > n {
		n = len(bs)
	}
	for i := 0; i < n; i++ {
		var av, bv int
		if i < len(as) {
			av = as[i]
		}
		if i < len(bs) {
			bv = bs[i]
		}
		if av != bv {
			if av < bv {
				return -1
			}
			return 1
		}
	}
	return 0
}

// VersionLess 报告 a 是否低于 b。a 为空或无法解析时返回 false（不拦）。
func VersionLess(a, b string) bool {
	a, b = strings.TrimSpace(a), strings.TrimSpace(b)
	if a == "" || b == "" {
		return false
	}
	if unparsable(a) || unparsable(b) {
		return false
	}
	return CompareVersions(a, b) < 0
}

// versionParts 把 "v1.2.3-beta+4" 拆成 [1,2,3]。
//
// 预发布后缀直接丢掉："1.2.3-beta" 与 "1.2.3" 视为同一版本。严格语义下
// beta 更低，但这里的用途是准入门槛，把预发布版当成更低会把内部测试包挡住。
func versionParts(s string) []int {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(strings.TrimPrefix(s, "v"), "V")
	if i := strings.IndexAny(s, "-+ "); i >= 0 {
		s = s[:i]
	}
	if s == "" {
		return nil
	}
	fields := strings.Split(s, ".")
	out := make([]int, 0, len(fields))
	for _, f := range fields {
		// 只取数字前缀："3a" → 3。
		end := 0
		for end < len(f) && f[end] >= '0' && f[end] <= '9' {
			end++
		}
		n, err := strconv.Atoi(f[:end])
		if err != nil {
			n = 0
		}
		out = append(out, n)
	}
	return out
}

// unparsable 判断版本串是否没有任何数字（例如 "dev"、"unknown"）。
//
// 没有数字的版本串（开发构建）一律不参与比较：`-ldflags -X main.version=dev`
// 的开发包必须能连自己的服务端，而不是被自己设的门禁挡住。
func unparsable(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= '0' && s[i] <= '9' {
			return false
		}
	}
	return true
}
