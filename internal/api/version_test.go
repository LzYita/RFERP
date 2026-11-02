package api

import "testing"

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.0.0", "1.0.0", 0},
		{"1.0.1", "1.0.0", 1},
		{"1.0.0", "1.0.1", -1},
		{"v1.2.3", "1.2.3", 0},
		{"1.2", "1.2.0", 0},
		{"1.2.3-beta", "1.2.3", 0},
		{"1.10.0", "1.9.9", 1},
		{"2", "1.99.99", 1},
	}
	for _, c := range cases {
		if got := CompareVersions(c.a, c.b); got != c.want {
			t.Errorf("CompareVersions(%q,%q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

// 开发构建（version=dev）与空版本号一律不拦：否则自己设的门禁会挡住自己的包。
func TestVersionLessSkipsUnparsable(t *testing.T) {
	for _, c := range []struct{ a, b string }{
		{"dev", "1.0.0"},
		{"", "1.0.0"},
		{"unknown", "1.0.0"},
		{"1.0.0", ""},
	} {
		if VersionLess(c.a, c.b) {
			t.Errorf("VersionLess(%q,%q) 应为 false", c.a, c.b)
		}
	}
}
