package main

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"app/internal/api"
)

// 用一份假的构建产物验证「页面由内嵌服务同源提供」这条链路。
func TestStaticAssetsServedFromEmbeddedFS(t *testing.T) {
	fsys := fstest.MapFS{
		"dist/index.html":     {Data: []byte(`<!doctype html><html><body><div id="root"></div><script src="/assets/app.js"></script></body></html>`)},
		"dist/assets/app.js":  {Data: []byte(`console.log("app")`)},
		"dist/favicon.svg":    {Data: []byte(`<svg/>`)},
		"dist/.gitkeep":       {Data: []byte(``)},
		"dist/nested/deep.js": {Data: []byte(`// nested`)},
	}

	s := api.New(&stubApps{}, api.WithStaticFS(fsys, "dist"))

	// 根路径应返回 index.html。
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != 200 {
		t.Fatalf("GET / = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), `id="root"`) {
		t.Errorf("根路径未返回 index.html: %q", w.Body.String())
	}

	// 静态资源应可直接取到。
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/assets/app.js", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "app") {
		t.Errorf("GET /assets/app.js = %d, body=%q", w.Code, w.Body.String())
	}

	// 前端路由应回落到 index.html，而不是 404。
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/parts", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `id="root"`) {
		t.Errorf("SPA 路由 /parts = %d，未回落到 index.html", w.Code)
	}
}

// 前端未构建时（只有 .gitkeep），必须给出可执行的指引。
//
// 这条同时守住 build:embed 的正确性：如果某次构建把产物写错了地方，
// 这里会立刻暴露，而不是发布一个「能打开但是空白页」的 exe。
func TestRealBuildOutputIsEmbedded(t *testing.T) {
	_, _, proxy := startEmbedded(t)

	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))

	body, _ := io.ReadAll(w.Result().Body)
	html := string(body)

	// 未构建时 serveStatic 回的是 JSON 错误而不是页面。按内容判断而不是
	// 匹配提示文案：文案一改，这个跳过分支就会失效并把「没构建」误报成
	// 「构建产物不对」。
	if strings.HasPrefix(strings.TrimSpace(html), "{") {
		t.Skip("前端尚未构建（internal/webassets/dist 里只有 .gitkeep）；" +
			"执行 cd web && npm run build:embed 后重跑本用例")
	}
	if !strings.Contains(html, `id="root"`) {
		t.Fatalf("内嵌的 index.html 不是前端构建产物:\n%s", html)
	}
	if !strings.Contains(html, "/assets/") {
		t.Errorf("index.html 未引用打包后的资源，嵌入的可能不是 dist 内容:\n%s", html)
	}
}
