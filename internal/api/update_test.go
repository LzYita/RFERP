package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 未配置更新源时：端点必须拒绝，且不发任何网络请求。
// 本地部署默认就是这个状态，静默去联网是行为错误。
func TestUpdateDisabledWithoutURL(t *testing.T) {
	s := New(&sessionApps{})
	h := s.Handler()

	// check 不需要会话：未登录时也只应回答"未启用"，不能泄露任何东西。
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/update/check", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("check = %d", w.Code)
	}
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out["enabled"] != false {
		t.Errorf("未配置更新源时 enabled 应为 false，实际 %v", out["enabled"])
	}

	// download / apply 需要会话（与其它写端点一致），未登录时先 401。
	// 这条断言的是鉴权顺序，不是更新源——两者都要成立。
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/api/update/download", strings.NewReader("{}")))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("未登录 download = %d，应 401", w.Code)
	}

	// 已登录但未配置更新源：必须是 400，且不触网。
	sess := issueAdmin(t, s)
	w = postJSON(t, s, "POST", "/api/update/download", sess, map[string]any{})
	if w.Code != http.StatusBadRequest {
		t.Errorf("已登录但未配置更新源 download = %d，应 400（不得发起网络请求）", w.Code)
	}
}

// apply 端点会替换用户正在运行的 exe。缺 confirm 必须拒绝——
// 这是"下载"和"动手"之间唯一的闸门，不能省。
func TestUpdateApplyRequiresExplicitConfirm(t *testing.T) {
	s := New(&sessionApps{}, WithUpdate("https://example.invalid/releases.json", "1.3.0"))
	sess := issueAdmin(t, s)

	cases := []struct {
		name string
		body string
	}{
		{"没带 confirm", `{"path":"C:/tmp/x.zip"}`},
		{"confirm 为 false", `{"path":"C:/tmp/x.zip","confirm":false}`},
		{"confirm 缺省", `{}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := postJSON(t, s, "POST", "/api/update/apply", sess, json.RawMessage(c.body))
			if w.Code != http.StatusBadRequest {
				t.Fatalf("apply = %d，必须 400：未确认时绝不能替换程序文件", w.Code)
			}
		})
	}
}
