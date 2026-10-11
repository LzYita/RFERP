package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log"
	"net/http"
	"strings"
	"time"

	"app/internal/model"
)

// 会话策略（D-005）。
//
// 三个维度缺一不可：
//   - 绝对上限 12h：即使一直有人在用，也必须重新登录一次。
//   - 空闲超时 30min：滑动续期。仓管可能连续几小时录单，不能按绝对空闲掐断。
//   - 单用户单会话：新登录踢掉旧会话。否则改密码后旧 token 仍可用。
//
// 会话只存在内存里：进程重启即全部失效。这既简化了实现，也让
// 「桌面端每次启动都是干净身份」成为默认行为。
const (
	sessionAbsoluteTTL = 12 * time.Hour
	sessionIdleTTL     = 30 * time.Minute
	sessionSweepEvery  = 5 * time.Minute
)

type session struct {
	Token string
	User  *model.User
	// UserID 冗余一份，便于按用户踢下线时不必解引用 User。
	UserID int64
	// CreatedAt 是绝对上限的起点，不随使用滑动。
	CreatedAt time.Time
	// LastSeenAt 每次成功鉴权都前移，是空闲超时的依据。
	LastSeenAt time.Time
	// ExpiresAt 为绝对上限时刻。
	ExpiresAt time.Time
}

// expired 判断会话在 now 时刻是否已失效。
// 空闲与绝对两个条件任一满足即失效。
func (s *session) expired(now time.Time) bool {
	return now.After(s.ExpiresAt) || now.After(s.LastSeenAt.Add(sessionIdleTTL))
}

// issueSession 为 u 建立会话，并踢掉该用户已有的其它会话（单用户单会话）。
func (s *Server) issueSession(u *model.User) (string, error) {
	var raw [24]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	tok := hex.EncodeToString(raw[:])
	now := time.Now()

	s.mu.Lock()
	defer s.mu.Unlock()
	// 单用户单会话：新登录作废旧会话。改密码后旧 token 立刻失效。
	for t, sess := range s.sessions {
		if sess.UserID == u.ID {
			delete(s.sessions, t)
		}
	}
	s.sessions[tok] = &session{
		Token:      tok,
		User:       u,
		UserID:     u.ID,
		CreatedAt:  now,
		LastSeenAt: now,
		ExpiresAt:  now.Add(sessionAbsoluteTTL),
	}
	return tok, nil
}

// userFromRequest 解析 Bearer token 并做空闲/绝对双重校验。
// 校验通过时前移 LastSeenAt（滑动续期）。
func (s *Server) userFromRequest(r *http.Request) (*model.User, bool) {
	tok := bearerToken(r)
	if tok == "" {
		return nil, false
	}
	now := time.Now()

	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[tok]
	if !ok || sess.expired(now) {
		if ok {
			delete(s.sessions, tok)
		}
		return nil, false
	}
	sess.LastSeenAt = now
	return sess.User, true
}

// bearerToken 从 Authorization 头取出 token，取不到返回空串。
func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if h == "" || !strings.HasPrefix(h, "Bearer ") {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
}

// revokeToken 作废单个 token。登出用。
func (s *Server) revokeToken(tok string) bool {
	if tok == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.sessions[tok]
	delete(s.sessions, tok)
	return ok
}

// RevokeUserSessions 作废某用户的全部会话，返回被作废的条数。
//
// 权限点存在会话快照里（userFromRequest 返回登录时的 User，不回查数据库），
// 所以管理员改了角色后，旧会话仍按旧角色继续放行。改角色时调用本方法强制下线，
// 让新权限立即生效。管理员改的是自己时同样会被踢下线，重新登录即可。
func (s *Server) RevokeUserSessions(userID int64) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for t, sess := range s.sessions {
		if sess.UserID == userID {
			delete(s.sessions, t)
			n++
		}
	}
	return n
}

// sweepSessions 清理已失效会话，返回清理条数。供定期任务与测试调用。
func (s *Server) sweepSessions(now time.Time) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for t, sess := range s.sessions {
		if sess.expired(now) {
			delete(s.sessions, t)
			n++
		}
	}
	return n
}

// StartSessionJanitor 定期清理失效会话，直到 ctx 结束。
//
// 没有它的话 map 只在「该 token 再次被使用」时才被惰性删除，
// 而再也不会回来的 token 会永久驻留——长期运行的 server 会无界增长。
//
// 调用方应在开始监听前启动它，并在 ctx 取消时停止。
func (s *Server) StartSessionJanitor(ctx context.Context) {
	go func() {
		t := time.NewTicker(sessionSweepEvery)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-t.C:
				if n := s.sweepSessions(now); n > 0 {
					log.Printf("session janitor: 清理失效会话 %d 条", n)
				}
			}
		}
	}()
}

// activeSessions 返回当前有效会话数，仅用于测试与诊断。
func (s *Server) activeSessions() int {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, sess := range s.sessions {
		if !sess.expired(now) {
			n++
		}
	}
	return n
}

// handleLogout 作废当前 token。
//
// 之前没有这个端点：客户端「退出登录」只清掉本地 token，
// 服务端的会话条目永久留存——拿着旧 token 的人仍能继续用。
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	s.revokeToken(bearerToken(r))
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
