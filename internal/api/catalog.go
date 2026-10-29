package api

import (
	"encoding/json"
	"errors"
	"fmt"
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
		// 409 而不是 400：请求本身没问题，是当前数据状态不允许删。
		// 带上 products，界面直接把「被哪些产品用了」列出来，不用去猜错误文案。
		var inBOM *usecase.PartInBOMError
		if errors.As(err, &inBOM) {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error":    inBOM.Error(),
				"products": inBOM.Products,
			})
			return
		}
		// 追溯记录引用：同样的 409，但给的是「哪些批次、各几条」——
		// 这些记录不能删，界面据此建议改「停用」。
		var inTrace *usecase.PartInTraceError
		if errors.As(err, &inTrace) {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error":   inTrace.Error(),
				"batches": inTrace.Batches,
				"records": inTrace.Records(),
			})
			return
		}
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

func (s *Server) handleUpdateBOM(w http.ResponseWriter, r *http.Request) {
	in, ok := decodeBOMUpdate(w, r)
	if !ok {
		return
	}
	u, _ := s.userFromRequest(r)
	name := operatorName(u)
	out, err := s.apps.UpdateBOMItem(&model.BOMItem{
		ID:          in.ID,
		Quantity:    in.Quantity,
		LossRate:    in.LossRate,
		UseMode:     in.UseMode,
		Replaceable: in.Replaceable,
		Version:     in.Version,
		Operator:    &name,
	})
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
	u, _ := s.userFromRequest(r)
	if err := s.apps.AddSkipPart(body.BatchID, body.PartID, operatorName(u)); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleListTempParts(w http.ResponseWriter, r *http.Request) {
	bid, err := strconv.ParseInt(r.URL.Query().Get("batch_id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid batch_id")
		return
	}
	rows, err := s.apps.ListTempParts(bid)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

// handleAddTempPart 加（或改数量）一个临时零件。库存不够时服务端拒绝，
// 错误文案点名还占着库存的批次——前端只负责把这句话展示出来。
func (s *Server) handleAddTempPart(w http.ResponseWriter, r *http.Request) {
	var body struct {
		BatchID  int64    `json:"batch_id"`
		PartID   int64    `json:"part_id"`
		Qty      float64  `json:"qty"`
		UseMode  *int     `json:"use_mode"`
		Quantity *float64 `json:"quantity"`
		LossRate *float64 `json:"loss_rate"`
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	u, _ := s.userFromRequest(r)
	if err := s.apps.AddTempPart(usecase.AddTempPartInput{
		BatchID:  body.BatchID,
		PartID:   body.PartID,
		Qty:      body.Qty,
		UseMode:  body.UseMode,
		Quantity: body.Quantity,
		LossRate: body.LossRate,
		Operator: operatorName(u),
	}); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleRemoveTempPart(w http.ResponseWriter, r *http.Request) {
	bid, err1 := strconv.ParseInt(r.URL.Query().Get("batch_id"), 10, 64)
	pid, err2 := strconv.ParseInt(r.URL.Query().Get("part_id"), 10, 64)
	if err1 != nil || err2 != nil {
		writeErr(w, http.StatusBadRequest, "invalid batch_id/part_id")
		return
	}
	u, _ := s.userFromRequest(r)
	if err := s.apps.RemoveTempPart(bid, pid, operatorName(u)); err != nil {
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
	u, _ := s.userFromRequest(r)
	if err := s.apps.RemoveSkipPart(bid, pid, operatorName(u)); err != nil {
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

// exportAuditRange 解析审计导出的日期区间。
//
// 返回的 end 是**内部边界**（已顺延一天，含当天），endUser 是**用户填的那一天** ——
// 文件名要用后者：用户看到 `audit_log_20261001_20261013.csv` 才知道自己导的是哪一段，
// 而 `…_20260930` 会让他以为自己选错了日期。
func (s *Server) exportAuditRange(w http.ResponseWriter, r *http.Request) (start, end, endUser time.Time, ok bool) {
	var body struct {
		Start string `json:"start"`
		End   string `json:"end"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return time.Time{}, time.Time{}, time.Time{}, false
	}
	start, err := parseExportDate(body.Start)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return time.Time{}, time.Time{}, time.Time{}, false
	}
	endUser, err = parseExportDate(body.End)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return time.Time{}, time.Time{}, time.Time{}, false
	}
	// 区间含两端：界面选的是"从这一天到那一天"，用户要的是这两天都包含。
	end = endUser
	if !end.Before(start) {
		end = end.AddDate(0, 0, 1)
	}
	return start, end, endUser, true
}

// runAuditExport 生成审计 CSV 并**落到数据目录**，返回目录、文件名、内容与条数。
//
// 两个端点共用它：界面的"导出"与"下载"拿到的是同一份东西，
// 分成两处写就会出现"界面上说保存到了 A，下载下来的却是 B"。
func (s *Server) runAuditExport(start, end, endUser time.Time) (dir, name string, data []byte, count int, err error) {
	// 与旧版一致：导出\audit_log\<时间戳>\（ui/backup.go:577）
	dir = filepath.Join(paths.ExportDir(), exportSubdirAuditLog, time.Now().Format("20060102_150405"))
	if mkErr := os.MkdirAll(dir, 0o755); mkErr != nil {
		return "", "", nil, 0, fmt.Errorf("创建导出目录: %w", mkErr)
	}
	name = fmt.Sprintf("audit_log_%s_%s.csv", start.Format("20060102"), endUser.Format("20060102"))
	full := filepath.Join(dir, name)

	count, err = s.apps.ExportAuditLogCSV(start, end, full)
	if err != nil {
		return "", "", nil, 0, err
	}
	data, err = os.ReadFile(full)
	if err != nil {
		return "", "", nil, 0, err
	}
	return dir, name, data, count, nil
}

// handleExportAudit 导出审计日志：**只回报位置**，不把内容塞进下载。
//
// 与 handleExportAll 对齐。界面上写的是"保存到 …"，用户照这句话去目录找文件；
// 顺手再往浏览器的「下载」里丢一份，只会让人多出一个不知道是什么的文件，
// 而且每次导出会多出一份重复数据。
func (s *Server) handleExportAudit(w http.ResponseWriter, r *http.Request) {
	start, end, endUser, ok := s.exportAuditRange(w, r)
	if !ok {
		return
	}
	dir, name, _, count, err := s.runAuditExport(start, end, endUser)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"dir":   dir,
		"files": []string{name},
		"count": count,
	})
}

// handleDownloadAudit 把审计 CSV 作为文件流返回。
//
// 只给**确实需要字节**的调用方：Go 侧客户端（远程模式）与远程界面形态。
// 主机形态下文件就在用户自己机器上，走 handleExportAudit 报位置即可。
func (s *Server) handleDownloadAudit(w http.ResponseWriter, r *http.Request) {
	start, end, endUser, ok := s.exportAuditRange(w, r)
	if !ok {
		return
	}
	_, name, data, count, err := s.runAuditExport(start, end, endUser)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition",
		"attachment; filename*=UTF-8''"+urlEscape(name))
	w.Header().Set("X-Count", strconv.Itoa(count))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// sessionUser 已在 auth 中间件校验过登录，这里再取一次会话用户。
func sessionUser(_ http.ResponseWriter, r *http.Request, s *Server) *model.User {
	u, _ := s.userFromRequest(r)
	return u
}
