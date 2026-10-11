package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"app/internal/auth"
	"app/internal/model"
)

// rememberStore 的时效规则。核心是「滑动续期 + 硬上限」：
// 没有硬上限的话，持续被使用的凭证永不过期。
func TestRememberIdleSlidesButHardCapHolds(t *testing.T) {
	rs := newRememberStore("")
	u := &model.User{ID: 1, Username: "admin", Role: string(auth.RoleAdmin)}
	tok, err := rs.issue(u)
	if err != nil {
		t.Fatal(err)
	}

	// 空闲超期：凭证失效
	rs.mu.Lock()
	rs.byID[u.ID].LastUsed = time.Now().Add(-rememberIdleTTL - time.Minute)
	rs.mu.Unlock()
	if _, ok := rs.exchange(tok); ok {
		t.Error("空闲超过 30 天后凭证仍可用")
	}

	// 重新签发，验证滑动续期
	tok2, _ := rs.issue(u)
	base := time.Now()
	for i := 0; i < 5; i++ {
		if _, ok := rs.exchange(tok2); !ok {
			t.Fatalf("第 %d 次使用失败：滑动续期没生效", i)
		}
		rs.mu.Lock()
		rs.byID[u.ID].LastUsed = time.Now().Add(-rememberIdleTTL + time.Hour)
		rs.mu.Unlock()
	}
	rs.mu.Lock()
	e := rs.byID[u.ID]
	rs.mu.Unlock()
	if e.IssuedAt.Add(rememberMaxTTL).Sub(base) > rememberMaxTTL+time.Minute {
		t.Errorf("硬上限被续期推后：签发起 %v 后仍超过上限", e.IssuedAt.Sub(base))
	}
	if time.Since(e.LastUsed) > rememberIdleTTL {
		t.Errorf("LastSeen 未随使用前移，落后 %v", time.Since(e.LastUsed))
	}
}

// 硬上限到了就是到了，不因使用而延后。
func TestRememberHardCapCannotBeExtended(t *testing.T) {
	rs := newRememberStore("")
	u := &model.User{ID: 2, Username: "a"}
	tok, _ := rs.issue(u)

	rs.mu.Lock()
	e := rs.byID[u.ID]
	origCap := e.IssuedAt
	e.IssuedAt = time.Now().Add(-rememberMaxTTL + time.Hour) // 硬上限已很近
	rs.mu.Unlock()

	rs.exchange(tok) // 使用一次

	rs.mu.Lock()
	after := rs.byID[u.ID].IssuedAt
	rs.mu.Unlock()
	if after.After(origCap) {
		t.Errorf("使用把硬上限从 %v 推后到 %v", origCap, after)
	}
}

// 过期必须彻底清除，不能让每次启动都白试一次。
func TestRememberSweepRemovesOnlyExpired(t *testing.T) {
	rs := newRememberStore("")
	live, _ := rs.issue(&model.User{ID: 10})
	expired, _ := rs.issue(&model.User{ID: 11})
	rs.mu.Lock()
	rs.byID[11].LastUsed = time.Now().Add(-rememberIdleTTL - time.Hour)
	rs.mu.Unlock()

	now := time.Now()
	if n := rs.sweep(now); n != 1 {
		t.Errorf("sweep 清理 %d 条, want 1", n)
	}
	if _, ok := rs.byIDToken(live); !ok {
		t.Error("有效凭证被误删")
	}
	if _, ok := rs.byIDToken(expired); ok {
		t.Error("过期凭证未清除")
	}
}

func (rs *rememberStore) byIDToken(tok string) (*rememberEntry, bool) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	for _, e := range rs.byID {
		if e.Token == tok {
			return e, true
		}
	}
	return nil, false
}

// 改密码 / 停用 / 改角色必须让凭证失效，否则「记住登录」会绕过这些控制。
func TestRevokeRememberForUser(t *testing.T) {
	dir := t.TempDir()
	s := New(&sessionApps{}, WithRemember(filepath.Join(dir, "remember.bin")))
	u := &model.User{ID: 42, Username: "wh", Role: string(auth.RoleWarehouse)}
	tok, _ := s.remember.issue(u)

	s.revokeRememberForUser(u.ID)
	if _, ok := s.remember.exchange(tok); ok {
		t.Error("凭证被撤销后仍可换取会话")
	}
	if n := s.remember.activeCount(); n != 0 {
		t.Errorf("撤销后仍有 %d 条凭证", n)
	}
}

// 端点串起来：保存 -> 读出 -> 换取会话 -> 磁盘副本被清掉。
func TestRememberEndpointFlow(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AppData", dir)
	s := New(&sessionApps{}, WithRemember(filepath.Join(dir, "remember.bin")))
	sess := issueAdmin(t, s)
	u := &model.User{ID: 1, Username: "admin", Role: string(auth.RoleAdmin), Status: 1}

	rt, err := s.remember.issue(u)
	if err != nil {
		t.Fatal(err)
	}

	// save
	w := postJSON(t, s, "POST", "/api/remember/save", sess,
		map[string]any{"token": rt, "user": map[string]any{"id": 1}})
	if w.Code != http.StatusOK {
		t.Fatalf("save = %d, body=%s", w.Code, w.Body.String())
	}

	// 磁盘上必须是密文，绝不能出现 token 明文
	raw, err := readFileAt(filepath.Join(dir, "remember.bin"))
	if err != nil {
		t.Fatalf("未落盘: %v", err)
	}
	if bytes.Contains(raw, []byte(rt)) {
		t.Error("磁盘上出现了 token 明文：DPAPI 加密没生效")
	}

	// load
	w = getReq(t, s, "/api/remember/load")
	var got struct {
		Token    string `json:"token"`
		Username string `json:"username"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got.Token != rt {
		t.Errorf("load 返回的 token 不一致")
	}
	if got.Username != "admin" {
		t.Errorf("load 未回填用户名: %q", got.Username)
	}

	// exchange：换取会话，并清掉磁盘副本
	w = postJSON(t, s, "POST", "/api/remember/exchange", "", map[string]any{"token": rt})
	if w.Code != http.StatusOK {
		t.Fatalf("exchange = %d, body=%s", w.Code, w.Body.String())
	}
	var ex struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &ex)
	if ex.Token == "" || ex.Token == rt {
		t.Error("exchange 未换出新的会话 token")
	}
	// 磁盘副本必须保留：删了「记住登录」就只能用一次，
	// 第二次启动时文件已不在，用户又得输密码。
	if _, err := readFileAt(filepath.Join(dir, "remember.bin")); err != nil {
		t.Error("换取会话后磁盘副本被删除：第二次启动就无法再记住")
	}
}

// 凭证失效时 exchange 返回 401，且顺手清掉没用的凭证。
func TestRememberExchangeExpiredReturns401(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AppData", dir)
	s := New(&sessionApps{}, WithRemember(filepath.Join(dir, "remember.bin")))
	u := &model.User{ID: 1, Username: "admin"}
	tok, _ := s.remember.issue(u)

	s.mu.Lock()
	s.remember.byID[u.ID].LastUsed = time.Now().Add(-rememberIdleTTL - time.Hour)
	s.mu.Unlock()

	w := postJSON(t, s, "POST", "/api/remember/exchange", "", map[string]any{"token": tok})
	if w.Code != http.StatusUnauthorized {
		t.Errorf("exchange = %d, want 401", w.Code)
	}
	if n := s.remember.activeCount(); n != 0 {
		t.Errorf("失效凭证未被清除，剩 %d 条", n)
	}
}

// save 端点必须校验会话与用户身份，否则任何人写进磁盘都是别人的凭证。
func TestRememberSaveRequiresMatchingSession(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AppData", dir)
	s := New(&sessionApps{}, WithRemember(filepath.Join(dir, "remember.bin")))
	sess := issueAdmin(t, s)

	w := postJSON(t, s, "POST", "/api/remember/save", sess,
		map[string]any{"token": "x", "user": map[string]any{"id": 999}})
	if w.Code != http.StatusUnauthorized {
		t.Errorf("冒用他人 ID = %d, want 401", w.Code)
	}
	if _, err := readFileAt(filepath.Join(dir, "remember.bin")); err == nil {
		t.Error("被拒绝的请求仍落了盘")
	}
}

func getReq(t *testing.T, s *Server, path string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", path, nil))
	return w
}

func readFileAt(p string) ([]byte, error) { return os.ReadFile(p) }

// 回归：程序重启后「记住登录」必须仍然可用。
//
// 之前的实现把凭证只放在内存表里，进程一重启表就空了，exchange() 必然 401，
// 表现为「勾了记住登录也没用」。磁盘上那份 DPAPI 密文才是凭证的唯一记录，
// 启动时必须 restore 回内存表。
func TestRememberSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AppData", dir)

	// 第一次运行：登录并勾选记住
	s1 := New(&sessionApps{}, WithRemember(filepath.Join(dir, "remember.bin")))
	u := &model.User{ID: 7, Username: "wh", Role: string(auth.RoleWarehouse)}
	tok, _ := s1.remember.issue(u)
	if err := s1.remember.saveToDisk(u, tok); err != nil {
		t.Fatalf("落盘失败: %v", err)
	}

	// 第二次运行：全新进程，内存表是空的
	// New() 内部会 restore，所以这里表里应当已经有那条凭证。
	s2 := New(&sessionApps{}, WithRemember(filepath.Join(dir, "remember.bin")))
	if n := s2.remember.activeCount(); n != 1 {
		t.Fatalf("restore 未生效，凭证数 = %d, want 1", n)
	}

	// 旧凭证应当还能换出会话
	got, ok := s2.remember.exchange(tok)
	if !ok {
		t.Fatal("重启后凭证失效：这就是「勾了也白勾」的原因")
	}
	if got.Username != "wh" {
		t.Errorf("换出的是 %q, want wh", got.Username)
	}
}

// 回归：换出会话后不能删掉磁盘副本，否则「记住」只能用一次。
func TestExchangeKeepsDiskCopy(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AppData", dir)
	u := &model.User{ID: 8, Username: "a", Role: string(auth.RoleAdmin), Status: 1}
	s := New(&sessionApps{users: []model.User{*u}}, WithRemember(filepath.Join(dir, "remember.bin")))
	tok, _ := s.remember.issue(u)
	if err := s.remember.saveToDisk(u, tok); err != nil {
		t.Fatal(err)
	}

	w := postJSON(t, s, "POST", "/api/remember/exchange", "", map[string]any{"token": tok})
	if w.Code != http.StatusOK {
		t.Fatalf("exchange = %d", w.Code)
	}
	if _, err := readFileAt(filepath.Join(dir, "remember.bin")); err != nil {
		t.Error("换取会话后磁盘副本被删除：第二次启动就无法再记住了")
	}
}

// 硬上限必须跨重启保留：IssuedAt 持久化，不随进程重启重置。
func TestRememberHardCapSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AppData", dir)

	s1 := New(&sessionApps{}, WithRemember(filepath.Join(dir, "remember.bin")))
	u := &model.User{ID: 9, Username: "old"}
	tok, _ := s1.remember.issue(u)
	// 把签发时间推到接近硬上限
	s1.remember.mu.Lock()
	s1.remember.byID[u.ID].IssuedAt = time.Now().Add(-rememberMaxTTL + time.Hour)
	issued := s1.remember.byID[u.ID].IssuedAt
	s1.remember.mu.Unlock()
	if err := s1.remember.saveToDisk(u, tok); err != nil {
		t.Fatal(err)
	}

	s2 := New(&sessionApps{}, WithRemember(filepath.Join(dir, "remember.bin"))) // 重启
	s2.remember.mu.Lock()
	e := s2.remember.byID[u.ID]
	s2.remember.mu.Unlock()
	if e == nil {
		t.Fatal("凭证未恢复")
	}
	// 持久化到秒级即可——一年上限不需要亚秒精度。
	if d := e.IssuedAt.Sub(issued); d > time.Second || d < -time.Second {
		t.Errorf("签发时间被重置：存的是 %v，原为 %v（差 %v）；硬上限会变成无限", e.IssuedAt, issued, d)
	}
}

// --- 安全回归：以下两条对应本轮安全排查发现的真实问题 ---

// 服务器形态（cmd/server）监听全网卡且没有启动 token 网关。
// 若在那里挂载 remember 端点，任何能连到端口的人都能取走明文长期凭证，
// 无需密码直接冒用该用户。remember 必须默认关闭、只在桌面形态显式开启。
func TestRememberEndpointsDisabledWithoutWithRemember(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AppData", dir)
	u := &model.User{ID: 1, Username: "admin", Role: string(auth.RoleAdmin), Status: 1}

	// 与 cmd/server 同样的装配方式：不传 WithRemember。
	s := New(&sessionApps{users: []model.User{*u}})
	tok, _ := s.remember.issue(u)
	if err := s.remember.saveToDisk(u, tok); err != nil {
		t.Fatal(err)
	}

	h := s.Handler()
	for _, ep := range []struct {
		method, path string
	}{
		{"GET", "/api/remember/load"},
		{"POST", "/api/remember/exchange"},
		{"POST", "/api/remember/save"},
		{"POST", "/api/remember/forget"},
	} {
		req := httptest.NewRequest(ep.method, ep.path, strings.NewReader(`{"token":"`+tok+`"}`))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Errorf("%s %s = %d，服务器形态下该端点必须不存在", ep.method, ep.path, w.Code)
		}
	}
}

// 停用账号不能靠「记住登录」绕过。
//
// 凭证作废只在执行操作的那个进程里生效，MySQL 多人形态下别人机器上的
// remember.bin 不会被清。若换会话时不回库判活，已停用用户点一下就能进来。
func TestRememberExchangeRejectsDisabledAccount(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AppData", dir)
	u := &model.User{ID: 5, Username: "gone", Role: string(auth.RoleAdmin), Status: 1}
	s := New(&sessionApps{users: []model.User{*u}}, WithRemember(filepath.Join(dir, "remember.bin")))
	tok, _ := s.remember.issue(u)
	if err := s.remember.saveToDisk(u, tok); err != nil {
		t.Fatal(err)
	}

	// 管理员在另一个进程里停用了该账号：数据库里 status 变了。
	s.apps.(*sessionApps).users = []model.User{{ID: 5, Username: "gone", Role: string(auth.RoleAdmin), Status: 0}}

	w := postJSON(t, s, "POST", "/api/remember/exchange", "", map[string]any{"token": tok})
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("停用账号换会话 = %d，必须 401", w.Code)
	}
	if _, err := readFileAt(filepath.Join(dir, "remember.bin")); err == nil {
		t.Error("停用后磁盘凭证未清除，下次启动还会再试一次")
	}
}

// 角色在数据库里改过之后，换会话必须用**新的**角色建会话。
// 凭证里缓存的旧角色不能直接拿去发令牌，否则降权/升权都可能被旧值盖住。
func TestRememberExchangeUsesCurrentRole(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AppData", dir)
	u := &model.User{ID: 6, Username: "r", Role: string(auth.RoleAdmin), Status: 1}
	s := New(&sessionApps{users: []model.User{*u}}, WithRemember(filepath.Join(dir, "remember.bin")))
	tok, _ := s.remember.issue(u)

	// 库里已降为只读，凭证里仍写着 admin。
	s.apps.(*sessionApps).users = []model.User{{ID: 6, Username: "r", Role: string(auth.RoleViewer), Status: 1}}

	w := postJSON(t, s, "POST", "/api/remember/exchange", "", map[string]any{"token": tok})
	if w.Code != http.StatusOK {
		t.Fatalf("exchange = %d", w.Code)
	}
	var out struct {
		User model.User `json:"user"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.User.Role != string(auth.RoleViewer) {
		t.Errorf("会话角色 = %q，必须用库里的 %q，不能用凭证里缓存的 admin", out.User.Role, auth.RoleViewer)
	}
}
