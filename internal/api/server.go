// Package api 提供 HTTP 应用入口（Phase C）。
// 鉴权与操作人来自服务端会话，不信任客户端传入的 operator。
package api

import (
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"net/http"
	"strconv"
	"sync"

	"app/internal/auth"
	"app/internal/model"
	"app/internal/usecase"
)

// Server 暴露同一套用例的 HTTP 面（C1–C3）。
type Server struct {
	apps usecase.Applications

	// desc 供 /api/v1/serverinfo 使用。默认与 apps 相同，单独成字段是为了让该
	// 端点只依赖 ServerDescriptor —— 便于测试替换，也明确它不碰业务数据。
	desc usecase.ServerDescriptor

	// version 由进程注入（ldflags），用于 /api/v1/serverinfo 自报版本（D-016）。
	version string

	mu       sync.Mutex
	sessions map[string]*session

	// remember 保存「记住登录」凭证，与普通会话分开管理。
	remember *rememberStore

	// rememberEnabled 把「记住登录」限制在**单机桌面形态**。
	//
	// 原因：handleLoadRemember 会把明文长期凭证返回给调用方，而这几个端点
	// 刻意不挂 s.auth（使用时用户还没有会话）。它们的安全边界只有启动 token
	// 网关——即"同机其它程序拿不到 Cookie"。
	//
	// cmd/server 监听 :8080 全网卡、给多人 MySQL 形态用，且**没有**启动 token。
	// 若在那里挂载 remember，任何能连到端口的人都能取走本机记住的凭证，
	// 无需密码直接冒用该用户。所以它只能由桌面入口显式开启。
	rememberEnabled bool

	// rememberFile 覆盖凭证落盘路径；空值表示用 %AppData%\RFERP\remember.bin。
	rememberFile string

	// startupToken 非空时启用启动 token 门（阶段 A 内嵌服务）。
	// cmd/server 留空，走 Bearer + 网络边界。
	startupToken string

	// updater 承载软件更新。url 为空表示未配置更新源，此时不检查、不提示、
	// 也不发任何网络请求（本地部署不需要联网）。
	updater updateChecker

	// staticFS/staticDir 非空时挂载前端构建产物（阶段 A 内嵌服务用它同时提供 API 与页面）。
	staticFS  fs.FS
	staticDir string

	// bootstrapMu 串行化"首次建管理员"的计数+创建，
	// 避免并发请求同时看到 0 用户而建出两个初始管理员。
	bootstrapMu sync.Mutex
}

// Option 用于可选装配（保持 New 的旧调用点不变）。
type Option func(*Server)

// WithVersion 让服务端在 /api/v1/serverinfo 里自报版本。
func WithVersion(v string) Option {
	return func(s *Server) { s.version = v }
}

// WithStaticFS 挂载前端构建产物。
// 单 exe 发行传 go:embed 得到的 fs.FS；开发期传 os.DirFS("web/dist")。
// dir 是资源在 fsys 内的前缀（嵌入式布局下为 "dist"）。
func WithStaticFS(fsys fs.FS, dir string) Option {
	return func(s *Server) {
		s.staticFS = fsys
		s.staticDir = dir
	}
}

// WithRemember 开启「记住登录」，并指定凭证落盘路径。
//
// 传空路径表示用默认位置（%AppData%\RFERP\remember.bin）。
// 只有 cmd/desktop-wails（单机桌面形态，带启动 token 网关）才应该调用它；
// cmd/server 必须保持关闭——见 Server.rememberEnabled 的说明。
func WithRemember(path string) Option {
	return func(s *Server) {
		s.rememberEnabled = true
		s.rememberFile = path
	}
}

// WithUpdate 启用软件更新。url 为空（默认）表示不启用。
//
// 只有桌面入口该传：更新会替换用户正在运行的 exe，这是单机行为，
// 不该在 cmd/server 上被触发。
func WithUpdate(url, currentVersion string) Option {
	return func(s *Server) {
		s.updater.url = url
		s.updater.cur = currentVersion
	}
}

// New 用给定用例实现构造 API。
func New(apps usecase.Applications, opts ...Option) *Server {
	s := &Server{apps: apps, desc: apps, sessions: make(map[string]*session), remember: newRememberStore("")}
	for _, opt := range opts {
		opt(s)
	}
	// WithRemember 才能决定凭证存哪，所以要在 opts 之后才建 store。
	s.remember.file = s.rememberFile
	// 把磁盘上的记住凭证登记回内存表，否则「记住登录」跨启动必然失效。
	// 必须在 opts 之后：开不开、存哪，都由 WithRemember 决定。
	if s.rememberEnabled {
		s.restoreRemember()
	}
	return s
}

// Handler 返回根路由。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealth)
	// 未登录可读的服务端自描述（D-016）：供"接入服务器"预检使用。
	// 只暴露版本与数据库身份，不含任何业务数据。
	mux.HandleFunc("GET /api/v1/serverinfo", s.handleServerInfo)
	// 软件更新：查 → 下载 → 应用，三步分开。
	// 「应用」会替换正在运行的 exe，必须带 confirm；不配置更新源时这三个端点
	// 仍可调用，但一律返回 enabled:false / 400，不发任何网络请求。
	mux.HandleFunc("GET /api/update/check", s.handleCheckUpdate)
	// download/apply 必须带权限点：之前这里是空字符串，而 auth() 对空权限点
	// 直接放行 —— 任何登录用户（含只读 viewer）都能替换正在运行的程序。
	mux.HandleFunc("POST /api/update/download", s.auth(s.handleDownloadUpdate, "System.DownloadUpdate"))
	mux.HandleFunc("POST /api/update/apply", s.auth(s.handleApplyUpdate, "System.ApplyUpdate"))
	mux.HandleFunc("POST /api/login", s.handleLogin)
	// 空库首次部署：仅当无任何用户时可建初始管理员。
	mux.HandleFunc("POST /api/bootstrap-admin", s.handleBootstrapAdmin)
	// 未登录可读用户数，供首启判断（仅 count）。
	mux.HandleFunc("GET /api/users/count", s.handleUserCount)
	mux.HandleFunc("GET /api/me", s.auth(s.handleMe, ""))
	// 当前用户的模块权限：前端导航据此显示，不在前端另抄一份权限矩阵。
	mux.HandleFunc("GET /api/me/permissions", s.auth(s.handleMyPermissions, ""))

	// 记住登录。注意这三个端点**不**挂 s.auth：它们在还没有用户会话时使用，
	// 安全边界是启动 token 网关（同机其它程序拿不到 Cookie）。
	//
	// 因此仅在 WithRemember 开启时挂载：cmd/server 无启动 token，挂上去等于
	// 把明文长期凭证开放给网络上任何客户端。
	if s.rememberEnabled {
		mux.HandleFunc("POST /api/remember/save", s.handleSaveRemember)
		mux.HandleFunc("GET /api/remember/load", s.handleLoadRemember)
		mux.HandleFunc("POST /api/remember/forget", s.handleForgetRemember)
		mux.HandleFunc("POST /api/remember/exchange", s.handleExchangeRemember)
	}
	// 登出：作废服务端会话条目。缺这个端点时客户端只清本地 token，
	// 旧 token 在服务端仍然有效。
	mux.HandleFunc("DELETE /api/session", s.auth(s.handleLogout, ""))
	mux.HandleFunc("GET /api/parts", s.auth(s.handleListParts, "Catalog.ListParts"))
	mux.HandleFunc("POST /api/parts/stock-in", s.auth(s.handleStockIn, "Inventory.StockIn"))
	mux.HandleFunc("POST /api/parts/adjust-stock", s.auth(s.handleAdjustStock, "Inventory.AdjustStock"))
	mux.HandleFunc("GET /api/batches", s.auth(s.handleListBatches, "Production.ListBatches"))
	mux.HandleFunc("POST /api/batches/status", s.auth(s.handleUpdateBatchStatus, "Production.UpdateBatchStatus"))
	mux.HandleFunc("GET /api/audit/recent", s.auth(s.handleRecentAudit, "Audit.ListRecentAuditLogs"))
	mux.HandleFunc("GET /api/users", s.auth(s.handleListUsers, "Identity.ListUsers"))
	mux.HandleFunc("POST /api/users", s.auth(s.handleCreateUser, "Identity.CreateUser"))
	mux.HandleFunc("PUT /api/users", s.auth(s.handleUpdateUser, "Identity.UpdateUser"))
	mux.HandleFunc("DELETE /api/users", s.auth(s.handleDeleteUser, "Identity.DeleteUser"))
	mux.HandleFunc("POST /api/users/reset-password", s.auth(s.handleResetPassword, "Identity.ResetPassword"))
	// 改自己的密码：登录即可，处理器内部再校验只能改自己。
	mux.HandleFunc("POST /api/users/change-password", s.auth(s.handleChangePassword, ""))
	mux.HandleFunc("GET /api/products", s.auth(s.handleListProducts, "Catalog.ListProducts"))
	mux.HandleFunc("GET /api/products/get", s.auth(s.handleGetProduct, "Catalog.GetProduct"))
	mux.HandleFunc("POST /api/products", s.auth(s.handleCreateProduct, "Catalog.CreateProduct"))
	mux.HandleFunc("PUT /api/products", s.auth(s.handleUpdateProduct, "Catalog.UpdateProduct"))
	mux.HandleFunc("DELETE /api/products", s.auth(s.handleDeleteProduct, "Catalog.DeleteProduct"))
	mux.HandleFunc("GET /api/parts/get", s.auth(s.handleGetPart, "Catalog.GetPart"))
	mux.HandleFunc("POST /api/parts", s.auth(s.handleCreatePart, "Catalog.CreatePart"))
	mux.HandleFunc("PUT /api/parts", s.auth(s.handleUpdatePart, "Catalog.UpdatePart"))
	mux.HandleFunc("DELETE /api/parts", s.auth(s.handleDeletePart, "Catalog.DeletePart"))
	mux.HandleFunc("GET /api/bom", s.auth(s.handleListBOM, "Catalog.GetBOMByProduct"))
	mux.HandleFunc("POST /api/bom", s.auth(s.handleAddBOM, "Catalog.AddBOMItem"))
	mux.HandleFunc("DELETE /api/bom", s.auth(s.handleRemoveBOM, "Catalog.RemoveBOMItem"))
	mux.HandleFunc("GET /api/bom/validate", s.auth(s.handleValidateBOM, "Catalog.ValidateBOM"))
	mux.HandleFunc("POST /api/batches", s.auth(s.handleCreateBatch, "Production.CreateBatch"))
	mux.HandleFunc("POST /api/batches/revoke", s.auth(s.handleRevokeBatch, "Production.RevokeBatch"))
	mux.HandleFunc("GET /api/batches/skipped", s.auth(s.handleSkippedParts, "Production.GetSkippedParts"))
	mux.HandleFunc("POST /api/batches/skip", s.auth(s.handleAddSkip, "Production.AddSkipPart"))
	mux.HandleFunc("DELETE /api/batches/skip", s.auth(s.handleRemoveSkip, "Production.RemoveSkipPart"))
	mux.HandleFunc("POST /api/traces", s.auth(s.handleRecordTrace, "Trace.RecordTrace"))
	mux.HandleFunc("POST /api/traces/batch", s.auth(s.handleRecordTraces, "Trace.RecordTraces"))
	mux.HandleFunc("GET /api/traces/by-batch", s.auth(s.handleTraceByBatch, "Trace.GetTraceByBatch"))
	mux.HandleFunc("GET /api/traces/by-product", s.auth(s.handleTraceByProduct, "Trace.TraceByProduct"))
	mux.HandleFunc("GET /api/traces/by-part", s.auth(s.handleTraceByPart, "Trace.TraceByPart"))
	mux.HandleFunc("GET /api/audit", s.auth(s.handleAuditByRecord, "Audit.GetAuditLogs"))
	mux.HandleFunc("GET /api/stats", s.auth(s.handleStats, "Stats.GetStockStats"))
	mux.HandleFunc("POST /api/export/audit", s.auth(s.handleExportAudit, "Backup.ExportAuditLogCSV"))
	mux.HandleFunc("POST /api/backup", s.auth(s.handleBackupDatabase, "Backup.BackupDatabase"))
	mux.HandleFunc("POST /api/export/all", s.auth(s.handleExportAll, "Backup.ExportAllDataCSV"))

	// 前端构建产物（阶段 A 内嵌服务）。放在 API 路由之后，
	// /api/ 前缀不会被静态文件截走。
	if s.staticFS != nil {
		mux.Handle("/", serveStatic(s.staticFS, s.staticDir))
	}
	return s.withStartupGate(mux)
}

type apiUser struct {
	ID          int64  `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name,omitempty"`
	Role        string `json:"role"`
}

func toAPIUser(u *model.User) apiUser {
	out := apiUser{ID: u.ID, Username: u.Username, Role: u.Role}
	if u.DisplayName != nil {
		out.DisplayName = *u.DisplayName
	}
	return out
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// writeServerErr 把内部错误记入日志，只向客户端返回通用信息，
// 避免把 SQL 语句、表名等内部细节泄露出去。
func writeServerErr(w http.ResponseWriter, err error) {
	log.Printf("api: internal error: %v", err)
	writeErr(w, http.StatusInternalServerError, "internal error")
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleServerInfo(w http.ResponseWriter, r *http.Request) {
	writeServerInfo(w, s.desc, s.version)
}

// writeServerInfo 是 handleServerInfo 的可测核心：只依赖 ServerDescriptor，
// 因此测试可以传入极小的假实现，不必构造整套 Applications。
func writeServerInfo(w http.ResponseWriter, desc usecase.ServerDescriptor, version string) {
	if desc == nil {
		writeErr(w, http.StatusServiceUnavailable, "server descriptor unavailable")
		return
	}
	info, err := desc.Describe()
	if err != nil {
		writeServerErr(w, err)
		return
	}
	// 版本由进程自报；数据库事实由用例层回答（D-016）。
	info.AppVersion = version
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
		// Remember 为真时额外签发长期凭证（见 remember.go）。
		// 客户端拿它去本地端点 DPAPI 加密落盘，下次启动换会话用。
		Remember bool `json:"remember"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid login body")
		return
	}
	u, err := s.apps.Login(body.Username, body.Password)
	if err != nil || u == nil {
		writeErr(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	tok, err := s.issueSession(u)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "session unavailable")
		return
	}
	out := map[string]any{
		"token": tok,
		"user":  toAPIUser(u),
	}
	if body.Remember {
		rt, err := s.remember.issue(u)
		if err != nil {
			// 记住登录失败不该挡住正常登录：用户照样能用，只是下次要输密码。
			log.Printf("issue remember credential: %v", err)
		} else {
			out["remember_token"] = rt
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleBootstrapAdmin(w http.ResponseWriter, r *http.Request) {
	s.bootstrapMu.Lock()
	defer s.bootstrapMu.Unlock()

	n, err := s.apps.UserCount()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if n > 0 {
		writeErr(w, http.StatusConflict, "already initialized")
		return
	}
	var body struct {
		Username    string `json:"username"`
		Password    string `json:"password"`
		DisplayName string `json:"display_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	u, err := s.apps.CreateInitialAdmin(body.Username, body.Password, body.DisplayName)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, toAPIUser(u))
}

// auth 包装鉴权与用例权限点；opName 为空表示仅需登录。
func (s *Server) auth(next http.HandlerFunc, opName string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := s.userFromRequest(r)
		if !ok {
			writeErr(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		if opName != "" && !usecase.Allow(auth.RoleFromString(u.Role), opName) {
			writeErr(w, http.StatusForbidden, "forbidden")
			return
		}
		next(w, r)
	}
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	u, ok := s.userFromRequest(r)
	if !ok || u == nil {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	writeJSON(w, http.StatusOK, toAPIUser(u))
}

func (s *Server) handleListParts(w http.ResponseWriter, _ *http.Request) {
	parts, err := s.apps.ListParts()
	if err != nil {
		writeServerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, parts)
}

func (s *Server) handleStockIn(w http.ResponseWriter, r *http.Request) {
	u, _ := s.userFromRequest(r)
	var body struct {
		PartID int64   `json:"part_id"`
		Qty    float64 `json:"qty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	// C3：操作人来自会话，不用客户端字段。
	if err := s.apps.StockIn(usecase.StockInInput{
		PartID:   body.PartID,
		Qty:      body.Qty,
		Operator: operatorName(u),
	}); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleAdjustStock(w http.ResponseWriter, r *http.Request) {
	u, _ := s.userFromRequest(r)
	var body struct {
		PartID int64   `json:"part_id"`
		NewQty float64 `json:"new_qty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	if err := s.apps.AdjustStock(usecase.AdjustStockInput{
		PartID:   body.PartID,
		NewQty:   body.NewQty,
		Operator: operatorName(u),
	}); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleListBatches(w http.ResponseWriter, _ *http.Request) {
	list, err := s.apps.ListBatches()
	if err != nil {
		writeServerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleUpdateBatchStatus(w http.ResponseWriter, r *http.Request) {
	u, _ := s.userFromRequest(r)
	var body struct {
		ID     int64 `json:"id"`
		Status int   `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	if err := s.apps.UpdateBatchStatus(usecase.UpdateBatchStatusInput{
		ID:       body.ID,
		Status:   body.Status,
		Operator: operatorName(u),
	}); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleRecentAudit(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			writeErr(w, http.StatusBadRequest, "invalid limit")
			return
		}
		limit = n
	}
	logs, err := s.apps.ListRecentAuditLogs(limit)
	if err != nil {
		writeServerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, logs)
}

func operatorName(u *model.User) string {
	if u == nil {
		return ""
	}
	if u.DisplayName != nil && *u.DisplayName != "" {
		return *u.DisplayName
	}
	return u.Username
}

// ErrUnauthorized 供调用方判断（预留）。
var ErrUnauthorized = errors.New("unauthorized")
