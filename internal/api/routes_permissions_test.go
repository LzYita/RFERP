package api

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"app/internal/usecase"
)

// 路由 ↔ 权限点的契约。
//
// 这个文件当初是为了盯住 `POST /api/update/download` 与 `/apply` 那两条路由：
// 它们当时是 `s.auth(h, "")`，而 auth() 对空权限点直接放行——**任何登录用户
// （含只读）都能让服务端下载并替换正在运行的可执行文件**。update_authz_test.go
// 挡住了那两个具体端点，但挡不住"下次新增路由时又忘了挂权限点"。
//
// 2026-10-15：产品决定把更新对齐 Fyne 旧版，于是那两条路由**仍然是空权限点**
// （登录即可），安全边界改由签名/哈希/confirm 三道闸门承担（见 server.go）。
// 这个文件的价值没变——它仍然要求"每条空权限点的路由都要显式登记理由"：
//
//  1. 凡挂了 s.auth 却给空权限点的路由，必须在下面的白名单里显式列出——
//     新增一条会立刻失败，逼作者回答"这条为什么不需要权限点"。
//  2. 路由引用的权限点名必须真实存在于 usecase 权限表（挡住拼写错误——
//     写错名字时 Allow 返回 false，表现为"所有人都被 403"，很难查）。
//  3. 权限表里的每个权限点要么被某条路由引用，要么在"已知未接线"清单里
//     显式承认（挡住"定义了权限点却没接"这种半成品状态）。
//
// 直接读 server.go 的源码文本：路由是命令式注册的，ServeMux 不暴露清单。
// 与 internal/auth 的 nav 契约测试同一套做法。
const serverSrc = "server.go"

// 只做会话校验、不需要权限点的路由。每一条都要有理由。
var routesWithoutOpPoint = map[string]string{
	"GET /api/me":                     "读自己的身份，任何登录用户都有权知道",
	"GET /api/me/permissions":         "读自己的权限档位，同上",
	"GET /api/help":                   "内置离线帮助文档，四个角色一视同仁，不需要模块权限",
	"DELETE /api/session":             "登出，任何登录用户都能退出自己",
	"POST /api/users/change-password": "改**自己**的密码；handler 内强制 in.ID == 会话用户 ID（admin.go:107）",
	// 更新：对齐 Fyne 旧版，任何登录角色都能更新（2026-10-15）。
	// 边界不是角色而是签名与哈希：包必须由生产私钥签名、哈希与清单一致、
	// apply 只认本次下载刚校验过的文件且必须带 confirm。守住"未登录不行"
	// 的是 update_authz_test.go 的 TestUpdateWriteEndpointsRejectAnonymous。
	"POST /api/update/download": "登录即可装**只有生产私钥签得出**的更新包；见 server.go 接线处的注释",
	"POST /api/update/apply":    "同上，且必须 confirm:true；路径由服务端记住，不接受客户端指认",
}

// 完全不挂 s.auth 的路由。同样每条都要有理由。
var routesWithoutAuth = map[string]string{
	"GET /healthz":                "存活探针",
	"GET /api/v1/serverinfo":      "接入服务器前的预检，只回版本与数据库身份（D-016）",
	"GET /api/update/check":       "登录页要在登录前提示可更新；TestUpdateCheckStaysOpen 守着这条",
	"POST /api/login":             "登录本身",
	"POST /api/bootstrap-admin":   "空库首建管理员，由 UserCount()>0 → 409 守卫",
	"GET /api/users/count":        "首启判断是否需要初始化，只回数量",
	"POST /api/remember/save":     "记住登录：在无会话时使用，边界是启动 token 网关",
	"GET /api/remember/load":      "同上",
	"POST /api/remember/forget":   "同上",
	"POST /api/remember/exchange": "同上",
}

// 权限表里已定义、但还没有路由消费的权限点。
// 每一条要么是待办（Round 5 补端点），要么是有意不接线。
//
// Backup.DataDir / SetDataDir / RestoreDatabase / ClearDatabase 原先在这里
// （Round 5 待办），现已接上路由：GET /api/backup/files 与 GET /api/datadir →
// DataDir、POST /api/datadir → SetDataDir、POST /api/restore → RestoreDatabase、
// POST /api/clear → ClearDatabase。
var opPointsWithoutRoute = map[string]string{
	"Identity.UserCount":      "端点公开（首启判断），此权限点仅作登记",
	"Identity.ChangePassword": "路由用空权限点（改自己密码不限角色），此权限点仅作登记",
}

type routeEntry struct {
	route string // "METHOD /path"
	op    string // 权限点名；"" 表示挂了 s.auth 但空权限点
	auth  bool   // 是否挂了 s.auth
}

var handleFuncRe = regexp.MustCompile(`mux\.HandleFunc\("([A-Z]+ [^"]+)"\s*,\s*(.+?)\)\s*$`)
var opNameRe = regexp.MustCompile(`s\.auth\(.*,\s*"([^"]*)"\)`)

func parseRoutes(t *testing.T) []routeEntry {
	t.Helper()
	b, err := os.ReadFile(serverSrc)
	if err != nil {
		t.Fatalf("读取 %s 失败：%v", serverSrc, err)
	}
	var out []routeEntry
	for _, line := range strings.Split(string(b), "\n") {
		m := handleFuncRe.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		e := routeEntry{route: m[1]}
		if om := opNameRe.FindStringSubmatch(m[2]); om != nil {
			e.auth = true
			e.op = om[1]
		}
		out = append(out, e)
	}
	if len(out) < 30 {
		t.Fatalf("只解析出 %d 条路由——正则失配，测试会假通过", len(out))
	}
	return out
}

func TestEveryAuthedRouteDeclaresWhatItNeeds(t *testing.T) {
	for _, e := range parseRoutes(t) {
		if !e.auth {
			if _, ok := routesWithoutAuth[e.route]; !ok {
				t.Errorf("%s 没挂 s.auth，也不在 routesWithoutAuth 清单里——是漏了鉴权还是漏了登记？", e.route)
			}
			continue
		}
		if e.op == "" {
			if _, ok := routesWithoutOpPoint[e.route]; !ok {
				t.Errorf("%s 挂了 s.auth 但权限点为空，且不在 routesWithoutOpPoint 清单里——空权限点等于放行所有登录用户", e.route)
			}
		}
	}
}

// 反向：白名单里不能有已经不存在的路由，否则清单会烂掉、失去约束力。
func TestAllowlistsHaveNoStaleEntries(t *testing.T) {
	seen := map[string]bool{}
	for _, e := range parseRoutes(t) {
		seen[e.route] = true
	}
	for r := range routesWithoutAuth {
		if !seen[r] {
			t.Errorf("routesWithoutAuth 里的 %q 已不在路由表上，请删掉这条", r)
		}
	}
	for r := range routesWithoutOpPoint {
		if !seen[r] {
			t.Errorf("routesWithoutOpPoint 里的 %q 已不在路由表上，请删掉这条", r)
		}
	}
}

// 路由引用的权限点必须真实存在。拼错名字时 Allow 返回 false，
// 现象是"所有人都 403"，而不是任何形式的编译/运行错误。
func TestRouteOpPointsExistInTable(t *testing.T) {
	known := map[string]bool{}
	for _, p := range usecase.AllPermissions() {
		known[p.Name] = true
	}
	for _, e := range parseRoutes(t) {
		if e.op == "" {
			continue
		}
		if !known[e.op] {
			t.Errorf("%s 引用了权限点 %q，但 usecase 权限表里没有它（拼写错误会让所有人都 403）", e.route, e.op)
		}
	}
}

// 权限表里的每个权限点都要有归属：要么被路由引用，要么在"已知未接线"里承认。
func TestEveryOpPointIsWiredOrAcknowledged(t *testing.T) {
	used := map[string]bool{}
	for _, e := range parseRoutes(t) {
		if e.op != "" {
			used[e.op] = true
		}
	}
	var orphans []string
	for _, p := range usecase.AllPermissions() {
		if used[p.Name] {
			continue
		}
		if _, ok := opPointsWithoutRoute[p.Name]; ok {
			continue
		}
		orphans = append(orphans, p.Name)
	}
	sort.Strings(orphans)
	if len(orphans) > 0 {
		t.Errorf("以下权限点既没有路由引用、也没在 opPointsWithoutRoute 里承认：%v\n"+
			"要么接上路由，要么显式登记（半成品状态会让权限表失去可信度）", orphans)
	}
	for name := range opPointsWithoutRoute {
		if used[name] {
			t.Errorf("权限点 %q 已经被路由引用了，请从 opPointsWithoutRoute 里删掉", name)
		}
	}
}
