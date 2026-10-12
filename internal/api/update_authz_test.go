package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"app/internal/auth"
	"app/internal/model"
)

// 回归测试：/api/update/download 与 /api/update/apply 曾用 s.auth(h, "") 接线，
// 而 auth() 对空权限点会直接跳过角色判断 —— 任何登录用户（包括只读 viewer）
// 都能让服务端去下载并替换正在运行的可执行文件。
//
// 断言必须是**精确的 403**：403 只可能来自 usecase.Allow 的拒绝。早期版本只
// 断言"不是 200"，结果被下游的"未配置更新源"（400）掩盖，撤掉权限点后测试
// 依然通过 —— 等于没测。负控验证见提交说明。
func issueAs(t *testing.T, s *Server, role auth.Role) string {
	t.Helper()
	tok, err := s.issueSession(&model.User{ID: 1, Username: "u", Role: string(role), Status: 1})
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func TestUpdateWriteEndpointsRejectNonAdmin(t *testing.T) {
	cases := []struct {
		name string
		role auth.Role
		body any
	}{
		{"viewer 不能下载", auth.RoleViewer, nil},
		{"仓库员不能下载", auth.RoleWarehouse, nil},
		{"生产员不能下载", auth.RoleProduction, nil},
		{"viewer 不能应用（即使带 confirm）", auth.RoleViewer, map[string]any{"path": "C:/tmp/x.zip", "confirm": true}},
		{"仓库员不能应用（即使带 confirm）", auth.RoleWarehouse, map[string]any{"path": "C:/tmp/x.zip", "confirm": true}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// 配置一个"看起来可用"的更新源：否则下游会先以 400 短路，
			// 掩盖掉鉴权是否生效。
			s := New(&sessionApps{}, WithUpdate("https://example.invalid/releases.json", "1.3.0"))
			tok := issueAs(t, s, c.role)

			for _, path := range []string{"/api/update/download", "/api/update/apply"} {
				body := c.body
				if body == nil {
					body = map[string]any{"path": "C:/tmp/x.zip", "confirm": true}
				}
				w := postJSON(t, s, "POST", path, tok, body)
				if w.Code != http.StatusForbidden {
					t.Errorf("%s 对 %s 返回 %d，期望 403（权限绕过或闸门顺序有误）",
						path, c.role, w.Code)
				}
			}
		})
	}
}

// 管理员必须仍然能用 —— 否则上面的修复可能只是把功能一起关掉了。
func TestUpdateWriteEndpointsAllowAdmin(t *testing.T) {
	s := New(&sessionApps{}, WithUpdate("https://example.invalid/releases.json", "1.3.0"))
	tok := issueAs(t, s, auth.RoleAdmin)

	// apply 缺少已下载文件会失败，但必须失败在"文件不存在"而不是 403。
	w := postJSON(t, s, "POST", "/api/update/apply", tok,
		map[string]any{"path": "C:/definitely/not/here.zip", "confirm": true})
	if w.Code == http.StatusForbidden {
		t.Fatal("管理员被 403 拒绝：修复把正常功能也挡掉了")
	}
	if w.Code == http.StatusUnauthorized {
		t.Fatal("管理员会话未被识别")
	}
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
}

// check 仍然无需会话：登录页要能查，且未登录时只回"未配置"不弹窗。
// 这里同时守住"不要顺手给它加上 auth"。
//
// 不配 WithUpdate：一旦被加上 auth()，匿名请求无论有没有 URL 都会是 401，
// 所以这个断言照样成立，而省掉一次真实网络请求（配了 example.invalid 也要
// 等 20 秒 DNS 超时）。
func TestUpdateCheckStaysOpen(t *testing.T) {
	s := New(&sessionApps{})
	w := postJSON(t, s, "GET", "/api/update/check", "", nil)
	if w.Code == http.StatusUnauthorized {
		t.Fatal("check 被加了鉴权，登录页将无法提示可更新")
	}
}
