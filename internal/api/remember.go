package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"app/internal/model"
	"app/internal/secret"
)

// 记住登录（对应旧版 Fyne 的「记住登录，自动登录」）。
//
// 设计取舍，逐条都有理由：
//
//  1. **磁盘上没有密码，只有凭证。** 旧版 internal/auth/session.go 存的是
//     remembered{Username, Password} —— DPAPI 加密的明文密码。同机程序拿到后
//     密码即永久泄露。凭证可以随时被服务端作废，密码不行。
//
//  2. **凭证与普通会话分开。** 记住凭证只用于启动时换一次普通会话，之后
//     日常请求一律用短会话 token。长命秘密因此不出现在常规 API 流量里。
//
//  3. **滑动续期 + 硬上限。** 每次使用把 30 天往后推，但最多 1 年。
//     没有硬上限的话，一个持续被使用的凭证永不过期，磁盘上那份数据的
//     有效期就成了无限。
//
//  4. **DPAPI 的边界要说清。** 能挡住：别的 Windows 用户、拿到硬盘直接读、
//     离线拷贝。挡不住：以该用户身份运行的任何程序或恶意软件。这是
//     Windows 的公认基线，旧版也是这个水平，不是本功能引入的新风险。
const (
	rememberIdleTTL = 30 * 24 * time.Hour  // 每次使用往后推
	rememberMaxTTL  = 365 * 24 * time.Hour // 从首次签发起算的硬上限
)

// remembered 是「记住凭证」的持久形态。磁盘上只有这个，且经 DPAPI 加密。
//
// IssuedAt 必须持久化：硬上限是「从首次签发起算一年」。若只放内存，
// 程序一重启就从头计一���，凭证会永远不过期——那正是硬上限要防的事。
type remembered struct {
	UserID int64 `json:"user_id"`
	// Username 只为回填登录框，不是秘密；真正的凭证是 Token。
	Username string `json:"username"`
	Token    string `json:"token"`
	IssuedAt int64  `json:"issued_at"`
}

// rememberEntry 是服务端的有效凭证表。
type rememberEntry struct {
	Token string
	User  *model.User
	// IssuedAt 与硬上限同源，不随使用滑动。
	IssuedAt time.Time
	// LastUsed 是空闲超时的依据，每次使用前移。
	LastUsed time.Time
}

// expired 判断凭证在 now 时刻是否已失效。
// 硬上限从 IssuedAt 起算且**不随使用滑动**；空闲超时随每次使用前移。
func (e *rememberEntry) expired(now time.Time) bool {
	return now.After(e.IssuedAt.Add(rememberMaxTTL)) || now.After(e.LastUsed.Add(rememberIdleTTL))
}

// rememberStore 保存记住凭证。与普通会话分开：作废、过期、续期各走各的规则。
type rememberStore struct {
	mu   sync.Mutex
	byID map[int64]*rememberEntry
	// file 为空表示用 %AppData%\RFERP\remember.bin；由 WithRemember 注入。
	file string
}

func newRememberStore(file string) *rememberStore {
	return &rememberStore{byID: make(map[int64]*rememberEntry), file: file}
}

// findActiveUser 回库确认账号仍然存在且启用。
//
// 与 service.Login 的判活规则保持一致（Status 必须为 1）。
// 「记住登录」换会话这条路原本直接信任磁盘里的旧凭证，等于给
// "停用账号" 这道关开了个后门——必须在这里补上。
func (s *Server) findActiveUser(id int64) *model.User {
	users, err := s.apps.ListUsers()
	if err != nil {
		// 查不到就当无效：宁可让用户重新输密码，也不能在无法确认时放行。
		return nil
	}
	for i := range users {
		if users[i].ID == id && users[i].Status == 1 {
			return &users[i]
		}
	}
	return nil
}

// issue 为用户签发记住凭证。每个用户只保留一个。
func (rs *rememberStore) issue(u *model.User) (string, error) {
	var raw [24]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	tok := hex.EncodeToString(raw[:])
	now := time.Now()

	rs.mu.Lock()
	defer rs.mu.Unlock()
	rs.byID[u.ID] = &rememberEntry{
		Token:    tok,
		User:     u,
		IssuedAt: now,
		LastUsed: now,
	}
	return tok, nil
}

// restore 把磁盘上的凭证重新登记进内存表。
//
// 这是「记住登录」能跨启动生效的关键：进程重启后内存表是空的，
// 若不 restore，exchange() 永远找不到这个凭证，表现为「勾了也白勾」。
// 磁盘上那份 DPAPI 密文本来就是这个凭证的唯一记录。
func (rs *rememberStore) restore(r remembered) bool {
	issued := time.Unix(r.IssuedAt, 0)
	now := time.Now()
	if now.After(issued.Add(rememberMaxTTL)) {
		return false // 已过硬上限
	}
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if _, dup := rs.byID[r.UserID]; dup {
		return false
	}
	rs.byID[r.UserID] = &rememberEntry{
		Token:    r.Token,
		User:     &model.User{ID: r.UserID, Username: r.Username},
		IssuedAt: issued,
		LastUsed: now,
	}
	return true
}

// exchange 用凭证换一个新的普通会话。凭证有效则顺带续期。
func (rs *rememberStore) exchange(tok string) (*model.User, bool) {
	if tok == "" {
		return nil, false
	}
	now := time.Now()

	rs.mu.Lock()
	defer rs.mu.Unlock()
	for id, e := range rs.byID {
		if e.Token != tok {
			continue
		}
		if e.expired(now) {
			delete(rs.byID, id)
			return nil, false
		}
		// 滑动续期，但不得越过硬上限。
		e.LastUsed = now
		return e.User, true
	}
	return nil, false
}

// revoke 作废某用户的凭证。改密码、停用、改角色、主动退出都要调。
func (rs *rememberStore) revoke(userID int64) bool {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	_, ok := rs.byID[userID]
	delete(rs.byID, userID)
	return ok
}

// activeCount 仅用于测试与诊断。
func (rs *rememberStore) activeCount() int {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return len(rs.byID)
}

// ---- 磁盘存储（DPAPI 加密）----

// defaultRememberPath 返回默认的凭证落盘位置：%AppData%\RFERP\remember.bin。
func defaultRememberPath() string {
	dir, err := os.UserConfigDir()
	if err != nil || dir == "" {
		dir = "."
	}
	return filepath.Join(dir, "RFERP", "remember.bin")
}

// path 返回本 store 使用的凭证文件路径。
//
// 可经 WithRemember 覆盖，是为了让测试与多实例部署能各自隔离：默认位置在
// %AppData%，同机跑两份程序会抢同一个文件，测试还会污染用户真实的凭证。
func (rs *rememberStore) path() string {
	if rs.file != "" {
		return rs.file
	}
	return defaultRememberPath()
}

func (rs *rememberStore) saveToDisk(u *model.User, tok string) error {
	rs.mu.Lock()
	e, ok := rs.byID[u.ID]
	rs.mu.Unlock()
	if !ok || e.Token != tok {
		return errors.New("remember: 无此凭证")
	}
	b, err := json.Marshal(remembered{
		UserID:   u.ID,
		Username: u.Username,
		Token:    tok,
		IssuedAt: e.IssuedAt.Unix(),
	})
	if err != nil {
		return err
	}
	enc, err := secret.Protect(b)
	if err != nil {
		return err
	}
	p := rs.path()
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return err
	}
	return os.WriteFile(p, enc, 0600)
}

func (rs *rememberStore) loadFromDisk() (remembered, bool) {
	b, err := os.ReadFile(rs.path())
	if err != nil {
		return remembered{}, false
	}
	dec, err := secret.Unprotect(b)
	if err != nil {
		return remembered{}, false
	}
	var r remembered
	if err := json.Unmarshal(dec, &r); err != nil || r.Token == "" || r.UserID == 0 {
		return remembered{}, false
	}
	return r, true
}

func (rs *rememberStore) clearDisk() {
	_ = os.Remove(rs.path())
}

// ---- HTTP 端点 ----

// handleSaveRemember 把凭证用 DPAPI 加密后写盘。
//
// 不要求用户会话：它由登录页在拿到凭证后立刻调用，那一刻还没有会话。
// 安全边界靠启动 token 网关（同机其它程序拿不到 Cookie）。
func (s *Server) handleSaveRemember(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token string `json:"token"`
		User  struct {
			ID int64 `json:"id"`
		} `json:"user"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Token == "" || in.User.ID == 0 {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	u, ok := s.userFromRequest(r)
	if !ok || u.ID != in.User.ID {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if err := s.remember.saveToDisk(u, in.Token); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleLoadRemember 读出加密的凭证并明文返回给本进程内的页面。
//
// 只在本机有效：端点位于启动 token 网关之后，同机其它进程打不进来。
func (s *Server) handleLoadRemember(w http.ResponseWriter, r *http.Request) {
	rm, ok := s.remember.loadFromDisk()
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"token": nil})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"token":    rm.Token,
		"user_id":  rm.UserID,
		"username": rm.Username,
	})
}

// handleForgetRemember 清除磁盘上的凭证。主动退出登录时调用。
func (s *Server) handleForgetRemember(w http.ResponseWriter, r *http.Request) {
	s.remember.clearDisk()
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleExchangeRemember 用记住凭证换一个普通会话。
//
// **不删磁盘上的凭证**——删了「记住登录」就只能用一次：第二次启动时文件已不在。
// 磁盘副本要一直留到过期、被撤销，或用户主动退出登录为止。
func (s *Server) handleExchangeRemember(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	u, ok := s.remember.exchange(in.Token)
	if !ok {
		// 失效的凭证顺手清掉，避免每次启动都白试一次。
		s.remember.clearDisk()
		writeErr(w, http.StatusUnauthorized, "remember expired")
		return
	}
	// 换会话前必须回库确认账号仍然有效，并以**库里的现值**建会话。
	//
	// 凭证作废（改密码/停用/改角色）只发生在**执行该操作的那个进程**里；
	// MySQL 多人形态下，别人机器上那份 remember.bin 并不会被清掉。
	// 若这里不重新校验，已被停用的用户只要点一下「登录」就能换到新会话——
	// 等于把停用账号这道关从 remember 这条路上整个绕了过去。
	live := s.findActiveUser(u.ID)
	if live == nil {
		s.revokeRememberForUser(u.ID)
		s.remember.clearDisk()
		writeErr(w, http.StatusUnauthorized, "account disabled")
		return
	}
	tok, err := s.issueSession(live)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "session unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"token": tok,
		"user":  toAPIUser(live),
	})
}

// restoreRemember 在进程启动时把磁盘上的凭证登记回内存表。
//
// 不做这件事，「记住登录」在第二次启动时必然失效：内存表随进程清空，
// 而磁盘上那份 DPAPI 密文才是凭证的唯一记录。
func (s *Server) restoreRemember() {
	rm, ok := s.remember.loadFromDisk()
	if !ok {
		return
	}
	if s.remember.restore(rm) {
		log.Printf("remember: 已恢复本机记住的登录凭证（用户 %s）", rm.Username)
		return
	}
	// 超过硬上限或重复登记：清掉这份没用的文件。
	s.remember.clearDisk()
}

// revokeRememberForUser 在改密码、停用、改角色时作废凭证。
func (s *Server) revokeRememberForUser(userID int64) {
	if s.remember.revoke(userID) {
		if rm, ok := s.remember.loadFromDisk(); ok && rm.UserID == userID {
			s.remember.clearDisk()
		}
	}
}

// startRememberJanitor 定期清理失效凭证。凭证只在启动时用一次，不清理会累积。
func (s *Server) startRememberJanitor(ctx context.Context) {
	go func() {
		t := time.NewTicker(time.Hour)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-t.C:
				s.remember.sweep(now)
			}
		}
	}()
}

// sweep 清理失效凭证，返回清理条数。供定期任务与测试调用。
func (rs *rememberStore) sweep(now time.Time) int {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	n := 0
	for id, e := range rs.byID {
		if e.expired(now) {
			delete(rs.byID, id)
			n++
		}
	}
	return n
}
