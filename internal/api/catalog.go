package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"app/internal/model"
	"app/internal/paths"
	"app/internal/usecase"
)

func (s *Server) handleGetProduct(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	p, err := s.apps.GetProduct(id)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if p == nil {
		writeErr(w, http.StatusNotFound, "product not found")
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) handleUpdateProduct(w http.ResponseWriter, r *http.Request) {
	in, ok := decodeProductWrite(w, r)
	if !ok {
		return
	}
	u, _ := s.userFromRequest(r)
	name := operatorName(u)
	in.Operator = &name

	p, err := in.toProductForUpdate(s)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	out, err := s.apps.UpdateProduct(p)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleDeleteProduct(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := s.apps.DeleteProduct(id, operatorName(sessionUser(w, r, s))); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleGetPart(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	p, err := s.apps.GetPart(id)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if p == nil {
		writeErr(w, http.StatusNotFound, "part not found")
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) handleCreatePart(w http.ResponseWriter, r *http.Request) {
	in, ok := decodePartWrite(w, r)
	if !ok {
		return
	}
	u, _ := s.userFromRequest(r)
	name := operatorName(u)
	in.Operator = &name

	p := in.toPartForCreate()
	out, err := s.apps.CreatePart(p)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleUpdatePart(w http.ResponseWriter, r *http.Request) {
	in, ok := decodePartWrite(w, r)
	if !ok {
		return
	}
	u, _ := s.userFromRequest(r)
	name := operatorName(u)
	in.Operator = &name

	p, err := in.toPartForUpdate(s)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	out, err := s.apps.UpdatePart(p)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleDeletePart(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := s.apps.DeletePart(id, operatorName(sessionUser(w, r, s))); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleListBOM(w http.ResponseWriter, r *http.Request) {
	pid, err := strconv.ParseInt(r.URL.Query().Get("product_id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid product_id")
		return
	}
	items, err := s.apps.GetBOMByProduct(pid)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) handleAddBOM(w http.ResponseWriter, r *http.Request) {
	var b model.BOMItem
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	u, _ := s.userFromRequest(r)
	name := operatorName(u)
	b.Operator = &name
	out, err := s.apps.AddBOMItem(&b)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleRemoveBOM(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := s.apps.RemoveBOMItem(id, operatorName(sessionUser(w, r, s))); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleValidateBOM(w http.ResponseWriter, r *http.Request) {
	pid, err := strconv.ParseInt(r.URL.Query().Get("product_id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid product_id")
		return
	}
	ok, err := s.apps.ValidateBOM(pid)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"valid": ok})
}

func (s *Server) handleCreateBatch(w http.ResponseWriter, r *http.Request) {
	var b model.ProductBatch
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	u, _ := s.userFromRequest(r)
	name := operatorName(u)
	b.Operator = &name
	out, err := s.apps.CreateBatch(&b)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleRevokeBatch(w http.ResponseWriter, r *http.Request) {
	u, _ := s.userFromRequest(r)
	var body struct {
		ID int64 `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	if err := s.apps.RevokeBatch(usecase.RevokeBatchInput{ID: body.ID, Operator: operatorName(u)}); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleSkippedParts(w http.ResponseWriter, r *http.Request) {
	bid, err := strconv.ParseInt(r.URL.Query().Get("batch_id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid batch_id")
		return
	}
	ids, err := s.apps.GetSkippedParts(bid)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, ids)
}

func (s *Server) handleAddSkip(w http.ResponseWriter, r *http.Request) {
	var body struct {
		BatchID int64 `json:"batch_id"`
		PartID  int64 `json:"part_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	if err := s.apps.AddSkipPart(body.BatchID, body.PartID); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleRemoveSkip(w http.ResponseWriter, r *http.Request) {
	bid, err1 := strconv.ParseInt(r.URL.Query().Get("batch_id"), 10, 64)
	pid, err2 := strconv.ParseInt(r.URL.Query().Get("part_id"), 10, 64)
	if err1 != nil || err2 != nil {
		writeErr(w, http.StatusBadRequest, "invalid batch_id/part_id")
		return
	}
	if err := s.apps.RemoveSkipPart(bid, pid); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleRecordTrace(w http.ResponseWriter, r *http.Request) {
	var t model.BatchTrace
	if err := json.NewDecoder(r.Body).Decode(&t); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	out, err := s.apps.RecordTrace(&t)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleRecordTraces(w http.ResponseWriter, r *http.Request) {
	var traces []*model.BatchTrace
	if err := json.NewDecoder(r.Body).Decode(&traces); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	if err := s.apps.RecordTraces(traces); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleTraceByBatch(w http.ResponseWriter, r *http.Request) {
	bid, err := strconv.ParseInt(r.URL.Query().Get("batch_id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid batch_id")
		return
	}
	out, err := s.apps.GetTraceByBatch(bid)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleTraceByProduct(w http.ResponseWriter, r *http.Request) {
	out, err := s.apps.TraceByProduct(r.URL.Query().Get("batch_no"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleTraceByPart(w http.ResponseWriter, r *http.Request) {
	out, err := s.apps.TraceByPart(r.URL.Query().Get("part_code"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleAuditByRecord(w http.ResponseWriter, r *http.Request) {
	rid, err := strconv.ParseInt(r.URL.Query().Get("record_id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid record_id")
		return
	}
	out, err := s.apps.GetAuditLogs(r.URL.Query().Get("table"), rid)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	days := 30
	if v := r.URL.Query().Get("days"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			writeErr(w, http.StatusBadRequest, "invalid days")
			return
		}
		days = n
	}
	st, err := s.apps.GetStockStats(days)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// parseExportDate 接受审计导出请求里的日期。
//
// 两种格式都要收：
//   - "2006-01-02"：界面选的是**日期范围**，发过来就是纯日期
//   - RFC 3339：Go 侧客户端（client.go 远程模式）把 time.Time 序列化过来的形状
//
// 之前这里直接把字段声明成 time.Time，而 Go 的 time.Time 只认 RFC 3339，
// 于是界面发来的 "2026-10-13" 解析失败 → 400 "invalid body"。
// 一个只在真实使用中才暴露的格式错：两端的 Go 测试都各自通过，
// 因为没人从界面发过一次请求。
func parseExportDate(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("日期格式无法识别：%q（应为 2026-10-13 或 RFC 3339）", s)
}

func (s *Server) handleExportAudit(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Start string `json:"start"`
		End   string `json:"end"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	start, err := parseExportDate(body.Start)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	end, err := parseExportDate(body.End)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	// 区间含两端：界面选的是"从这一天到那一天"，用户要的是这两天都包含。
	if !end.Before(start) {
		end = end.AddDate(0, 0, 1)
	}

	// 除了给浏览器下载一份，也在数据目录里留一份 —— 与旧版一致
	// （ui/backup.go:577 写 导出\audit_log\<时间戳>\）。
	// 界面上写的是"保存到 …"，用户会照着这句话去那个目录找文件；
	// 只下载不留存，那句话就是假的。
	keepDir := filepath.Join(paths.ExportDir(), exportSubdirAuditLog, time.Now().Format("20060102_150405"))
	if err := os.MkdirAll(keepDir, 0o755); err != nil {
		log.Printf("api: export audit: create keep dir: %v", err)
	}
	tmpDir, err := os.MkdirTemp("", "rferp-audit-*")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer os.RemoveAll(tmpDir)
	tmp := filepath.Join(tmpDir, "audit.csv")
	count, err := s.apps.ExportAuditLogCSV(start, end, tmp)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	data, err := os.ReadFile(tmp)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	// 留一份到数据目录（失败只记日志：已经导出成功了，不该因此报错让用户以为没导出）
	keepName := fmt.Sprintf("audit_log_%s_%s.csv", start.Format("20060102"), start.Format("20060102"))
	if err := os.WriteFile(filepath.Join(keepDir, keepName), data, 0o600); err != nil {
		log.Printf("api: export audit: keep copy: %v", err)
	}

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition",
		"attachment; filename*=UTF-8''"+urlEscape(keepName))
	w.Header().Set("X-Count", strconv.Itoa(count))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// sessionUser 已在 auth 中间件校验过登录，这里再取一次会话用户。
func sessionUser(_ http.ResponseWriter, r *http.Request, s *Server) *model.User {
	u, _ := s.userFromRequest(r)
	return u
}
