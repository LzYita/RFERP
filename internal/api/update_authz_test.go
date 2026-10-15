package api

import (
	"net/http"
	"strings"
	"testing"

	"app/internal/auth"
	"app/internal/model"
	"app/internal/usecase"
)

// 软件更新的授权边界（2026-10-15 起与 Fyne 旧版对齐）。
//
// 变化：download / apply 从「仅管理员」改为「任何登录角色」。这与 2026-10-13
// 审计时的结论相反，那条结论的依据是"任何登录用户（含只读 viewer）都能让服务端
// 下载并替换正在运行的可执行文件"。产品决定对齐旧版——旧版的启动检查对任何登录
// 用户都会弹框——所以那半条结论作废，理由改为：
//
//	真正的边界不是角色，而是与角色无关的三道闸门（见 server.go 接线处的注释）：
//	签名（生产私钥 ed25519）、哈希（下载与 apply 前各核一次）、以及 apply 只认
//	"本次下载刚校验通过"的那个临时文件。非签名者无法让程序装上任何自己选的代码。
//	剩下的取舍是可用性取舍：任何登录角色都可能在他人录入数据时发起一次更新并
//	重启程序——这是本次明确接受的代价。
//
// 但**未登录仍然不行**，这才是这个文件现在要守的东西：auth() 只在挂上时校验会话，
// 权限点为空不等于不校验。任何时候有人把 s.auth 改成裸 handler，这里立刻失败。
func issueAs(t *testing.T, s *Server, role auth.Role) string {
	t.Helper()
	tok, err := s.issueSession(&model.User{ID: 1, Username: "u", Role: string(role), Status: 1})
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

// 四个角色都不该再被 403 挡住。
//
// 断言写成"落到 handler 里那个已知的下游错误"，而不是"不是 401 也不是 403"：
// 旧版那两条测试就是吃过"下游 400 把鉴权失败掩盖掉"的亏——只断言"不是 200"时，
// 撤掉权限点测试照样绿。这里必须证明请求**真的走进了 handler**：
//
//	download（未配置更新源）→ 400 "未配置更新地址"
//	apply   （没下载过）      → 400 "尚未下载更新包，请先下载"
//
// 顺带的好处：不配 WithUpdate 就没有清单地址，一次网络请求都不会发，
// 测试不依赖外网、不会因为 DNS 超时而慢成分钟级（曾经配 example.invalid，
// 每个角色要走满 5 次重试退避才返回）。
func TestUpdateWriteEndpointsAllowEveryRole(t *testing.T) {
	roles := []struct {
		name string
		role auth.Role
	}{
		{"管理员", auth.RoleAdmin},
		{"仓库员", auth.RoleWarehouse},
		{"生产员", auth.RoleProduction},
		{"只读 viewer", auth.RoleViewer},
	}
	wantMsg := map[string]string{
		"/api/update/download": "未配置更新地址",
		"/api/update/apply":    "尚未下载更新包",
	}
	for _, r := range roles {
		t.Run(r.name, func(t *testing.T) {
			s := New(&sessionApps{})
			tok := issueAs(t, s, r.role)

			for _, path := range []string{"/api/update/download", "/api/update/apply"} {
				w := postJSON(t, s, "POST", path, tok,
					map[string]any{"path": "C:/definitely/not/here.zip", "confirm": true})
				if w.Code != http.StatusBadRequest {
					t.Errorf("%s 对 %s 返回 %d，期望 400（走到 handler 的未配置分支）",
						r.role, path, w.Code)
					continue
				}
				if !strings.Contains(w.Body.String(), wantMsg[path]) {
					t.Errorf("%s 对 %s 的 400 内容 = %q，期望包含 %q——说明它没走到 handler，"+
						"403/鉴权仍在前面拦着", r.role, path, w.Body.String(), wantMsg[path])
				}
			}
		})
	}
}

// 匿名必须仍然拿不到：没有会话就没有"用户确认"这件事可依赖，
// 而 apply 会替换正在运行的程序。
//
// 这条断言的价值在于它挡住 `s.auth(h, "")` 被改回裸 handler 的写法——那时 403 与
// 放行都会消失，两个角色测试仍然全绿，文件看起来"测过了"。
func TestUpdateWriteEndpointsRejectAnonymous(t *testing.T) {
	s := New(&sessionApps{}, WithUpdate("https://example.invalid/releases.json", "1.3.0"))
	for _, path := range []string{"/api/update/download", "/api/update/apply"} {
		w := postJSON(t, s, "POST", path, "",
			map[string]any{"path": "C:/definitely/not/here.zip", "confirm": true})
		if w.Code != http.StatusUnauthorized {
			t.Errorf("匿名对 %s 返回 %d，期望 401（未登录不得触发替换程序文件）", path, w.Code)
		}
	}
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

// 权限表里不该再有 System.* ：更新走"登录即可"之后，这些点既没有路由引用，
// 也没有对应的页面，留着只会让读代码的人以为更新仍然是管理员专属。
func TestNoSystemPermissionPoints(t *testing.T) {
	for _, p := range usecase.AllPermissions() {
		if strings.HasPrefix(p.Name, "System.") {
			t.Errorf("权限表里还有 %q：更新已对所有登录角色开放，这个点应当删除", p.Name)
		}
	}
}

// apply 仍要求 confirm:true —— 这道门与角色无关，是"下载"到"动手"之间的用户闸门，
// 不该在放开权限时被顺手去掉。具体断言（confirm=false 时连 applier 都不许碰）在
// update_apply_test.go 的 TestApplyUpdateRequiresConfirm。
