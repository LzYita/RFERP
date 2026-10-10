package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"app/internal/auth"
	"app/internal/model"
	"app/internal/usecase"
)

// fakeAppsApps 满足 usecase.Applications 的最小桩；本文件只测会话，
// 用不到的方法全部 panic，避免「桩悄悄返回零值」掩盖问题。
type sessionApps struct {
	usecase.Applications
}

// user 造一个测试用户。role 取 auth.Role 常量，落库字段是字符串。
func user(id int64, name string, role auth.Role) *model.User {
	return &model.User{ID: id, Username: name, Role: string(role), Status: 1}
}

func newTestServer(t *testing.T) *Server {
	t.Helper()
	return New(&sessionApps{})
}

// issue 造一个已登录会话，返回 token。
func issue(t *testing.T, s *Server, u *model.User) string {
	t.Helper()
	tok, err := s.issueSession(u)
	if err != nil {
		t.Fatalf("issueSession: %v", err)
	}
	return tok
}

func getWithToken(t *testing.T, s *Server, method, path, tok string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, nil)
	if tok != "" {
		r.Header.Set("Authorization", "Bearer "+tok)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}

// 登出后旧 token 必须立刻失效。
// 之前没有 DELETE /api/session：客户端只清本地 token，服务端条目永久留存。
func TestLogoutInvalidatesToken(t *testing.T) {
	s := newTestServer(t)
	tok := issue(t, s, user(1, "admin", auth.RoleAdmin))

	if w := getWithToken(t, s, "GET", "/api/me", tok); w.Code != http.StatusOK {
		t.Fatalf("登出前 /api/me = %d, want 200", w.Code)
	}
	if w := getWithToken(t, s, "DELETE", "/api/session", tok); w.Code != http.StatusOK {
		t.Fatalf("DELETE /api/session = %d, want 200", w.Code)
	}
	if w := getWithToken(t, s, "GET", "/api/me", tok); w.Code != http.StatusUnauthorized {
		t.Errorf("登出后 /api/me = %d, want 401；旧 token 必须失效", w.Code)
	}
}

// 空闲超时：超过 30min 没有请求就失效。
func TestSessionExpiresAfterIdleTimeout(t *testing.T) {
	s := newTestServer(t)
	tok := issue(t, s, user(1, "admin", auth.RoleAdmin))

	s.mu.Lock()
	sess := s.sessions[tok]
	sess.LastSeenAt = time.Now().Add(-sessionIdleTTL - time.Minute)
	s.mu.Unlock()

	if w := getWithToken(t, s, "GET", "/api/me", tok); w.Code != http.StatusUnauthorized {
		t.Errorf("/api/me = %d, want 401；空闲超时后必须失效", w.Code)
	}
}

// 滑动续期：持续使用的会话不应被空闲超时掐断。
// 仓管可能连续几小时录单，按绝对空闲掐断是不可接受的。
func TestSessionSlidesOnEachUse(t *testing.T) {
	s := newTestServer(t)
	tok := issue(t, s, user(1, "admin", auth.RoleAdmin))

	// 每次请求前把 LastSeenAt 往前拨，模拟「一直在用」。
	for i := 0; i < 3; i++ {
		s.mu.Lock()
		s.sessions[tok].LastSeenAt = time.Now().Add(-sessionIdleTTL + time.Minute)
		s.mu.Unlock()

		if w := getWithToken(t, s, "GET", "/api/me", tok); w.Code != http.StatusOK {
			t.Fatalf("第 %d 次请求 = %d, want 200；持续使用不应被空闲超时掐断", i, w.Code)
		}
	}
	// 三次之后 LastSeenAt 应当已被本次请求前移到「刚刚」。
	s.mu.Lock()
	last := s.sessions[tok].LastSeenAt
	s.mu.Unlock()
	if time.Since(last) > time.Minute {
		t.Errorf("LastSeenAt 未被滑动续期，落后 %v", time.Since(last))
	}
}

// 绝对上限：即使一直有人在用，12h 后也必须重新登录。
func TestSessionExpiresAtAbsoluteLimit(t *testing.T) {
	s := newTestServer(t)
	tok := issue(t, s, user(1, "admin", auth.RoleAdmin))

	s.mu.Lock()
	sess := s.sessions[tok]
	sess.ExpiresAt = time.Now().Add(-time.Minute) // 绝对上限已过
	sess.LastSeenAt = time.Now()                  // 但刚刚还在用
	s.mu.Unlock()

	if w := getWithToken(t, s, "GET", "/api/me", tok); w.Code != http.StatusUnauthorized {
		t.Errorf("/api/me = %d, want 401；绝对上限优先于滑动续期", w.Code)
	}
}

// 单用户单会话：新登录作废旧会话。
func TestSingleSessionPerUser(t *testing.T) {
	s := newTestServer(t)
	u := user(7, "wh", auth.RoleWarehouse)
	first := issue(t, s, u)
	second := issue(t, s, u)

	if w := getWithToken(t, s, "GET", "/api/me", first); w.Code != http.StatusUnauthorized {
		t.Errorf("旧 token = %d, want 401；单用户单会话应作废旧会话", w.Code)
	}
	if w := getWithToken(t, s, "GET", "/api/me", second); w.Code != http.StatusOK {
		t.Errorf("新 token = %d, want 200", w.Code)
	}
	if got := s.activeSessions(); got != 1 {
		t.Errorf("有效会话数 = %d, want 1", got)
	}
}

// 改角色后强制下线：权限点存在会话快照里，不踢下线对方仍按旧角色通行。
func TestRevokeUserSessionsForcesRelogin(t *testing.T) {
	s := newTestServer(t)
	whTok := issue(t, s, user(7, "wh", auth.RoleWarehouse))
	adminTok := issue(t, s, user(1, "admin", auth.RoleAdmin))

	if n := s.RevokeUserSessions(7); n != 1 {
		t.Fatalf("RevokeUserSessions = %d, want 1", n)
	}
	if w := getWithToken(t, s, "GET", "/api/me", whTok); w.Code != http.StatusUnauthorized {
		t.Errorf("被改角色者 = %d, want 401；必须强制下线", w.Code)
	}
	// 别人的会话不受影响。
	if w := getWithToken(t, s, "GET", "/api/me", adminTok); w.Code != http.StatusOK {
		t.Errorf("他人会话 = %d, want 200；不应被牵连", w.Code)
	}
}

// 清扫协程依赖的纯函数：只清失效的，不误伤有效会话。
func TestSweepSessionsRemovesOnlyExpired(t *testing.T) {
	s := newTestServer(t)
	live := issue(t, s, user(1, "admin", auth.RoleAdmin))
	idle := issue(t, s, user(2, "a", auth.RoleAdmin))
	absolute := issue(t, s, user(3, "b", auth.RoleAdmin))

	now := time.Now()
	s.mu.Lock()
	s.sessions[idle].LastSeenAt = now.Add(-sessionIdleTTL - time.Minute)
	s.sessions[absolute].ExpiresAt = now.Add(-time.Minute)
	s.mu.Unlock()

	if n := s.sweepSessions(now); n != 2 {
		t.Errorf("sweepSessions = %d, want 2", n)
	}
	s.mu.Lock()
	_, stillThere := s.sessions[live]
	s.mu.Unlock()
	if !stillThere {
		t.Error("有效会话被误删")
	}
	if got := s.activeSessions(); got != 1 {
		t.Errorf("剩余有效会话 = %d, want 1", got)
	}
}

// 清扫协程必须能随 ctx 取消而退出，否则关不掉。
func TestSessionJanitorStopsWithContext(t *testing.T) {
	s := newTestServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	s.StartSessionJanitor(ctx)
	cancel()
	// 给协程一点时间退出；这里只验证不挂死，不做严格时序断言。
	time.Sleep(50 * time.Millisecond)
	if got := s.activeSessions(); got != 0 {
		t.Errorf("有效会话 = %d, want 0", got)
	}
}

// 无 Authorization 头时不得 panic，直接 401。
func TestMissingAuthorizationHeader(t *testing.T) {
	s := newTestServer(t)
	if w := getWithToken(t, s, "GET", "/api/me", ""); w.Code != http.StatusUnauthorized {
		t.Errorf("/api/me = %d, want 401", w.Code)
	}
	if w := getWithToken(t, s, "GET", "/api/me", "garbage"); w.Code != http.StatusUnauthorized {
		t.Errorf("畸形 token = %d, want 401", w.Code)
	}
}
