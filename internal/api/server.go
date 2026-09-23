// Package api 提供 HTTP 应用入口（Phase C）。
// 鉴权与操作人来自服务端会话，不信任客户端传入的 operator。
package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"app/internal/auth"
	"app/internal/model"
	"app/internal/usecase"
)

// Server 暴露同一套用例的 HTTP 面（C1–C3）。
type Server struct {
	apps usecase.Applications

	mu       sync.Mutex
	sessions map[string]*session
}

type session struct {
	Token     string
	User      *model.User
	ExpiresAt time.Time
}

// New 用给定用例实现构造 API。
func New(apps usecase.Applications) *Server {
	return &Server{apps: apps, sessions: make(map[string]*session)}
}

// Handler 返回根路由。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("POST /api/login", s.handleLogin)
	// 空库首次部署：仅当无任何用户时可建初始管理员。
	mux.HandleFunc("POST /api/bootstrap-admin", s.handleBootstrapAdmin)
	// 未登录可读用户数，供首启判断（仅 count）。
	mux.HandleFunc("GET /api/users/count", s.handleUserCount)
	mux.HandleFunc("GET /api/me", s.auth(s.handleMe, ""))
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
	mux.HandleFunc("POST /api/users/change-password", s.auth(s.handleChangePassword, "Identity.ChangePassword"))
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
	return mux
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

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
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
	writeJSON(w, http.StatusOK, map[string]any{
		"token": tok,
		"user":  toAPIUser(u),
	})
}

func (s *Server) handleBootstrapAdmin(w http.ResponseWriter, r *http.Request) {
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

func (s *Server) issueSession(u *model.User) (string, error) {
	var raw [24]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	tok := hex.EncodeToString(raw[:])
	s.mu.Lock()
	s.sessions[tok] = &session{Token: tok, User: u, ExpiresAt: time.Now().Add(12 * time.Hour)}
	s.mu.Unlock()
	return tok, nil
}

func (s *Server) userFromRequest(r *http.Request) (*model.User, bool) {
	h := r.Header.Get("Authorization")
	if h == "" || !strings.HasPrefix(h, "Bearer ") {
		return nil, false
	}
	tok := strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[tok]
	if !ok || time.Now().After(sess.ExpiresAt) {
		if ok {
			delete(s.sessions, tok)
		}
		return nil, false
	}
	return sess.User, true
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
