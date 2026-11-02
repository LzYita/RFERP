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
//   - 绝对上限 24h：即使一直有人在用，也必须重新登录一次。
//   - 空闲超时 4h：滑动续期。仓管可能连续几小时录单，不能按绝对空闲掐断。
//   - 单用户单会话：新登录踢掉旧会话。否则改密码后旧 token 仍可用。
//
// 2026-10-12 由用户调整，原为 12h / 30min。理由是 12h 会每天咬人一次：
// 「每天开机用几小时、第二天还要重登」对本地单机软件是日常场景，而风险等级
// 远低于服务器（无网络暴露、单机单用户）。空闲从 30min → 2h → 4h，用户
// 按实际操作节奏定的：中途离开去吃饭、去办事，回来不该被要求重登。
//
// 会话只存在内存里：进程重启即全部失效。这既简化了实现，也让
// 「桌面端每次启动都是干净身份」成为默认行为。
const (
	sessionAbsoluteTTL = 24 * time.Hour
	sessionIdleTTL     = 4 * time.Hour
	sessionSweepEvery  = 5 * time.Minute
	// deviceRecheckEvery 是会话回库确认设备状态的最小间隔（局域网版）。
	deviceRecheckEvery = time.Minute
)

type session struct {
	Token string
	User  *model.User
	// UserID 冗余一份，便于按用户踢下线时不必解引用 User。
	UserID int64
	// DeviceID 是登录时携带的设备指纹（局域网版）。
	// 空串表示单机形态（不启用设备门禁）——那种形态下没有设备头，
	// 强行要求会让现有单机用户全部登不进去。
	DeviceID string
	// DeviceCheckedAt 是上一次回库确认设备状态的时间。
	// 进程内撤销由 revoked 集合即时生效；跨进程（CLI）撤销靠这个字段
	// 每分钟兜底查一次库，把生效时间从"下次重启"压到一分钟内。
	DeviceCheckedAt time.Time
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
//
// deviceID 可选：局域网形态必传，单机形态不传。用可变参数而不是新增一个函数，
// 是为了让"单机不需要设备"这件事在调用点一眼可见，而不是到处传空串。
func (s *Server) issueSession(u *model.User, deviceID ...string) (string, error) {
	var raw [24]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	tok := hex.EncodeToString(raw[:])
	now := time.Now()
	dev := ""
	if len(deviceID) > 0 {
		dev = deviceID[0]
	}

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
		DeviceID:   dev,
		CreatedAt:  now,
		LastSeenAt: now,
		ExpiresAt:  now.Add(sessionAbsoluteTTL),
	}
	return tok, nil
}

// userFromRequest 解析 Bearer token 并做空闲/绝对双重校验。
// 校验通过时前移 LastSeenAt（滑动续期）。
//
// 局域网形态额外做两件事：会话绑定的设备必须与本次请求头一致，
// 且设备当前未被撤销。两者任一不满足都返回 401——token 本身没失效，
// 但"拿 token 的人不在那台机器上"，语义上就是未授权（FR-28）。
func (s *Server) userFromRequest(r *http.Request) (*model.User, bool) {
	tok := bearerToken(r)
	if tok == "" {
		return nil, false
	}
	now := time.Now()

	s.mu.Lock()
	sess, ok := s.sessions[tok]
	if !ok || sess.expired(now) {
		if ok {
			delete(s.sessions, tok)
		}
		s.mu.Unlock()
		return nil, false
	}
	sess.LastSeenAt = now
	u, boundDevice := sess.User, sess.DeviceID
	needRecheck := now.Sub(sess.DeviceCheckedAt) >= deviceRecheckEvery
	s.mu.Unlock()

	if s.deviceGuard != nil {
		got := normalizeDeviceID(r.Header.Get("X-RFERP-Device-ID"))
		if boundDevice == "" || got != boundDevice {
			return nil, false
		}
		if err := s.deviceGuard.ValidateSession(got); err != nil {
			return nil, false
		}
		// 周期回库复核：只覆盖"另一个进程把它撤销了"这一类，
		// 本进程撤销已在上面即时生效，不必每请求查库。
		if needRecheck {
			if v, ok := s.deviceGuard.(interface{ VerifyDevice(string) error }); ok {
				if err := v.VerifyDevice(got); err != nil {
					return nil, false
				}
				s.mu.Lock()
				sess.DeviceCheckedAt = now
				s.mu.Unlock()
			}
		}
	}
	return u, true
}

// revokeDeviceSessions 作废绑定到某台设备的全部会话，返回条数。
//
// 单用户单会话下通常只有一条，但主机与客户端可能用同一个账号——撤销一台
// 设备时把别的设备一并踢掉是不可接受的，所以按设备而不是按用户筛。
func (s *Server) revokeDeviceSessions(deviceID string) int {
	if deviceID == "" {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for t, sess := range s.sessions {
		if sess.DeviceID == deviceID {
			delete(s.sessions, t)
			n++
		}
	}
	return n
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
