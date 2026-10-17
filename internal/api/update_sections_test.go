package api

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"app/internal/update"
)

// /api/update/check 必须返回**已切好小节**的更新说明。
//
// 起因（2026-10-17）：登录后的更新提示原来是原生 MessageBox，而 MessageBox 画不了
// markdown、也没有滚动条，只能把窗口撑到把按钮顶出屏幕，`== 中文 ==` 这些标记还以
// 原文示人。改成网页弹窗后，弹窗与「设置 → 更新说明」读的是同一份 notes，
// 因此切分必须由服务端做一次（help.ParseNotes），不能让前端各自实现一遍。
//
// 断言要点：sections 非空、按中英两段切出、每段带 title 与 body。前端拿到它就能
// 直接交给 <Markdown> 渲染，不需要自己认 `==` 分隔行。
func TestCheckEndpointReturnsParsedSections(t *testing.T) {
	pinnedPub, pinnedPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	const notes = "RFERP v1.3.0\n\n== 中文 ==\n\n一、新功能\n\n- 侧栏新增「设置」入口\n\n" +
		"二、问题修复\n\n- 修复列表滚动跳回顶部\n\n== English ==\n\nFeatures\n\n- A new Settings entry\n"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m := update.Manifest{
			Version: "1.3.0",
			URL:     "http://127.0.0.1:1/pkg.zip",
			Size:    10,
			SHA256:  pkgSHA,
			Notes:   notes,
		}
		if err := m.Sign(pinnedPriv); err != nil {
			t.Error(err)
		}
		_ = json.NewEncoder(w).Encode(m)
	}))
	t.Cleanup(srv.Close)

	s := checkServer(t, pinnedPub, srv, srv)
	tok := issueAdmin(t, s)

	w := doJSON(t, s, "GET", "/api/update/check", tok, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("check = %d，期望 200：%s", w.Code, w.Body.String())
	}
	out := decodeBody(t, w)
	if out["hasUpdate"] != true {
		t.Fatalf("清单是更高版本，hasUpdate 应为 true：%v", out["hasUpdate"])
	}

	raw, ok := out["sections"].([]any)
	if !ok || len(raw) == 0 {
		t.Fatalf("响应里没有 sections：%v", out)
	}

	// 至少要切出中文那两节（一、新功能 / 二、问题修复）与英文那节。
	var titles []string
	for _, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("section 不是对象：%v", item)
		}
		if body, _ := m["body"].(string); body == "" {
			t.Errorf("有一节 body 是空的：%v", m)
		}
		if title, _ := m["title"].(string); title != "" {
			titles = append(titles, title)
		}
	}
	for _, want := range []string{"一、新功能", "二、问题修复", "Features"} {
		if !slices.Contains(titles, want) {
			t.Errorf("sections 里没有小节 %q，实际切出 %v", want, titles)
		}
	}

	// 关键：前端不该再拿到需要自己切的 `==` 分隔行。
	if b, _ := raw[0].(map[string]any)["body"].(string); strings.Contains(b, "==") {
		t.Errorf("第一节 body 里仍有 `==` 分隔标记，说明切分没做干净：%q", b)
	}
}

// 没有 notes 的清单不该 panic，sections 给空数组而不是缺字段 ——
// 前端可以无脑遍历，不必到处判断字段存不存在。
func TestCheckEndpointSectionsEmptyWhenNoNotes(t *testing.T) {
	pinnedPub, pinnedPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m := update.Manifest{Version: "1.3.0", URL: "http://127.0.0.1:1/pkg.zip", Size: 10, SHA256: pkgSHA}
		if err := m.Sign(pinnedPriv); err != nil {
			t.Error(err)
		}
		_ = json.NewEncoder(w).Encode(m)
	}))
	t.Cleanup(srv.Close)

	s := checkServer(t, pinnedPub, srv, srv)
	tok := issueAdmin(t, s)

	w := doJSON(t, s, "GET", "/api/update/check", tok, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("check = %d，期望 200：%s", w.Code, w.Body.String())
	}
	out := decodeBody(t, w)
	if out["hasUpdate"] != true {
		t.Fatalf("清单是更高版本，hasUpdate 应为 true：%v", out["hasUpdate"])
	}
	if _, ok := out["sections"]; !ok {
		t.Errorf("没有 notes 时 sections 字段应当存在（空数组），实际缺失：%v", out)
	}
}
