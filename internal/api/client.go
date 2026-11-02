package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"app/internal/model"
	"app/internal/usecase"
)

// Client 是远程用例适配（D1/D2）：UI 经 HTTP 访问主机上的同一 Service。
type Client struct {
	base       string
	hc         *http.Client
	tok        string
	generation uint64
	lastCount  string
	// device 是局域网版要求的设备身份（方案 §7.1）。单机形态留空——
	// 服务端没有设备门禁时这些头会被忽略，但也不能凭空编一个。
	device DeviceIdentity
	// mu 保护连接目标、凭据和设备身份，运行期切换与请求可能并发。
	mu sync.RWMutex
}

// RemoteDeployment 标记这是"业务数据在远端主机"的形态。
//
// 供 /api/datadir 之类的端点判断：客户端本机没有业务目录可展示，
// 返回路径只会让人以为备份在本地（FR-16 / R14）。
func (c *Client) RemoteDeployment() bool { return true }

// ServerURL 返回当前主机地址。
func (c *Client) ServerURL() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.base
}

// SetServerURL 切换主机地址（运行期生效，不必重启）。
//
// 之所以能热切：Client 的每个方法都是"读一次 base、发一次请求"，
// 没有连接池或长连接需要重建——局域网 HTTP 下切换就是换个地址。
func (c *Client) SetServerURL(u string) {
	c.mu.Lock()
	c.base = strings.TrimRight(strings.TrimSpace(u), "/")
	c.tok = ""
	c.generation++
	c.mu.Unlock()
}

// DeviceIdentity 是客户端随请求上报的设备事实。
type DeviceIdentity struct {
	ID      string
	Name    string
	Version string
	OS      string
}

// SetDevice 设置设备身份。必须在登录前调用：登录本身就要带设备头，
// 服务端据此登记并计额度。
func (c *Client) SetDevice(d DeviceIdentity) {
	c.mu.Lock()
	c.device = d
	c.mu.Unlock()
}

// newRequest 统一构造请求并补上头。
//
// 收在一处的原因：Authorization 与设备头必须"每一条请求都带"。之前
// do/doBytes 各写一遍，新增一个头就得记得改两处，漏掉的那条表现为
// "偶尔 401"，是最难查的一类缺陷。
func (c *Client) newRequest(method, path string, in any) (*http.Request, error) {
	var body io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(raw)
	}
	c.mu.RLock()
	base, token, device := c.base, c.tok, c.device
	c.mu.RUnlock()
	req, err := http.NewRequestWithContext(context.Background(), method, base+path, body)
	if err != nil {
		return nil, err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if device.ID != "" {
		req.Header.Set("X-RFERP-Device-ID", device.ID)
		req.Header.Set("X-RFERP-Device-Name", device.Name)
		req.Header.Set("X-RFERP-Client-Version", device.Version)
		req.Header.Set("X-RFERP-Client-OS", device.OS)
	}
	return req, nil
}

var _ usecase.Applications = (*Client)(nil)

// NewClient 创建远程客户端；base 例如 http://192.168.1.10:8080 。
func NewClient(base string) *Client {
	return &Client{
		base: strings.TrimRight(base, "/"),
		hc:   &http.Client{Timeout: 30 * time.Second},
	}
}

// SetToken 注入已记住的会话（D3）。
func (c *Client) SetToken(tok string) {
	c.mu.Lock()
	c.tok = tok
	c.mu.Unlock()
}

// Token 返回当前会话。
func (c *Client) Token() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.tok
}

func (c *Client) CheckSession(userID int64) error {
	var user model.User
	if err := c.do(http.MethodGet, "/api/me", nil, &user); err != nil {
		return err
	}
	if user.ID != userID {
		return &APIError{Status: http.StatusUnauthorized, Message: "upstream session changed"}
	}
	return nil
}

func (c *Client) do(method, path string, in any, out any) error {
	return c.doWithHeaders(method, path, in, out, nil)
}

// doWithHeaders 在统一请求之上加一次性头（目前只有初始化令牌用到）。
//
// 做成参数而不是 Client 上的可变字段：令牌只在"首建管理员"这一次请求里有效，
// 挂在 Client 上会跟着后续所有请求走，也会在切换主机地址后误发到新主机。
func (c *Client) doWithHeaders(method, path string, in any, out any, extra map[string]string) error {
	req, err := c.newRequest(method, path, in)
	if err != nil {
		return err
	}
	for k, v := range extra {
		req.Header.Set(k, v)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		return responseError(resp.StatusCode, raw)
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, out)
}

func (c *Client) doBytes(method, path string, in any) ([]byte, error) {
	req, err := c.newRequest(method, path, in)
	if err != nil {
		return nil, err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	c.lastCount = resp.Header.Get("X-Count")
	if resp.StatusCode >= 400 {
		return nil, responseError(resp.StatusCode, raw)
	}
	return raw, nil
}

func responseError(status int, raw []byte) *APIError {
	var problem struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	_ = json.Unmarshal(raw, &problem)
	if problem.Error == "" {
		problem.Error = strings.TrimSpace(string(raw))
	}
	if problem.Error == "" {
		problem.Error = http.StatusText(status)
	}
	return &APIError{Status: status, Code: problem.Code, Message: problem.Error}
}

// APIError 保留 HTTP 状态，便于 UI/上层判断。
type APIError struct {
	Status  int
	Code    string
	Message string
}

// IsCode 判断错误是否属于某个错误契约码。
func (e *APIError) IsCode(code string) bool { return e != nil && e.Code == code }

func (e *APIError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return fmt.Sprintf("api error %d", e.Status)
}

// ---- Identity ----

func (c *Client) UserCount() (int, error) {
	var out struct {
		Count int `json:"count"`
	}
	if err := c.do(http.MethodGet, "/api/users/count", nil, &out); err != nil {
		return 0, err
	}
	return out.Count, nil
}

func (c *Client) Login(username, password string) (*model.User, error) {
	// Login 不依赖既有 token。
	c.mu.Lock()
	generation := c.generation
	loginClient := &Client{base: c.base, hc: c.hc, device: c.device}
	c.tok = ""
	c.mu.Unlock()
	var out struct {
		Token string     `json:"token"`
		User  model.User `json:"user"`
	}
	in := map[string]string{"username": username, "password": password}
	if err := loginClient.do(http.MethodPost, "/api/login", in, &out); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.generation != generation {
		return nil, &APIError{Status: http.StatusUnauthorized, Message: "server changed during login"}
	}
	c.tok = out.Token
	u := out.User
	return &u, nil
}

func (c *Client) CreateInitialAdmin(username, password, displayName string) (*model.User, error) {
	return c.createInitialAdmin(username, password, displayName, "")
}

// CreateInitialAdminWithToken 带初始化令牌首建管理员（局域网主机，FR-41）。
//
// 服务端在空库启动时生成令牌并要求 `X-Bootstrap-Token`；客户端没有别的
// 途径把它传过去，因此单独这一个方法承担"透传令牌"的职责。
func (c *Client) CreateInitialAdminWithToken(username, password, displayName, token string) (*model.User, error) {
	return c.createInitialAdmin(username, password, displayName, token)
}

func (c *Client) createInitialAdmin(username, password, displayName, token string) (*model.User, error) {
	// 空库引导：无会话也可调用；已有用户则服务端 409。
	var u model.User
	in := map[string]string{"username": username, "password": password, "display_name": displayName}
	var extra map[string]string
	if token != "" {
		extra = map[string]string{"X-Bootstrap-Token": token}
	}
	if err := c.doWithHeaders(http.MethodPost, "/api/bootstrap-admin", in, &u, extra); err != nil {
		return nil, err
	}
	// bootstrap 只建号不发 token，必须再登录，否则后续请求全是 unauthorized。
	return c.Login(username, password)
}

func (c *Client) CreateUser(in usecase.CreateUserInput) (*model.User, error) {
	var u model.User
	if err := c.do(http.MethodPost, "/api/users", in, &u); err != nil {
		return nil, err
	}
	return &u, nil
}

func (c *Client) ListUsers() ([]model.User, error) {
	var out []model.User
	err := c.do(http.MethodGet, "/api/users", nil, &out)
	return out, err
}

func (c *Client) UpdateUser(in usecase.UpdateUserInput) error {
	return c.do(http.MethodPut, "/api/users", in, nil)
}

func (c *Client) ResetPassword(in usecase.ResetPasswordInput) error {
	return c.do(http.MethodPost, "/api/users/reset-password", in, nil)
}

func (c *Client) ChangePassword(in usecase.ChangePasswordInput) error {
	return c.do(http.MethodPost, "/api/users/change-password", in, nil)
}

func (c *Client) DeleteUser(id int64) error {
	return c.do(http.MethodDelete, "/api/users?id="+strconv.FormatInt(id, 10), nil, nil)
}

// ---- Catalog / Inventory（第一版：读 + 库存写；档案写可后续补路由）----

func (c *Client) CreateProduct(p *model.Product) (*model.Product, error) {
	var out model.Product
	if err := c.do(http.MethodPost, "/api/products", p, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) GetProduct(id int64) (*model.Product, error) {
	var out model.Product
	if err := c.do(http.MethodGet, "/api/products/get?id="+strconv.FormatInt(id, 10), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) ListProducts() ([]model.Product, error) {
	var out []model.Product
	err := c.do(http.MethodGet, "/api/products", nil, &out)
	return out, err
}

func (c *Client) UpdateProduct(p *model.Product) (*model.Product, error) {
	var out model.Product
	if err := c.do(http.MethodPut, "/api/products", p, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) DeleteProduct(id int64, operator string) error {
	return c.do(http.MethodDelete, "/api/products?id="+strconv.FormatInt(id, 10), nil, nil)
}

func (c *Client) CreatePart(p *model.Part) (*model.Part, error) {
	var out model.Part
	if err := c.do(http.MethodPost, "/api/parts", p, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) GetPart(id int64) (*model.Part, error) {
	var out model.Part
	if err := c.do(http.MethodGet, "/api/parts/get?id="+strconv.FormatInt(id, 10), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) ListParts() ([]model.Part, error) {
	var out []model.Part
	err := c.do(http.MethodGet, "/api/parts", nil, &out)
	return out, err
}

func (c *Client) UpdatePart(p *model.Part) (*model.Part, error) {
	var out model.Part
	if err := c.do(http.MethodPut, "/api/parts", p, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) DeletePart(id int64, operator string) error {
	return c.do(http.MethodDelete, "/api/parts?id="+strconv.FormatInt(id, 10), nil, nil)
}

func (c *Client) StockIn(in usecase.StockInInput) (string, error) {
	// operator 由服务端会话注入；此处字段仅为兼容签名。
	body := map[string]any{"part_id": in.PartID, "qty": in.Qty, "note": in.Note}
	var out struct {
		Status  string `json:"status"`
		BatchNo string `json:"batch_no"`
	}
	if err := c.do(http.MethodPost, "/api/parts/stock-in", body, &out); err != nil {
		return "", err
	}
	return out.BatchNo, nil
}

func (c *Client) ListPartInbounds(partID int64) ([]model.PartInbound, error) {
	var out []model.PartInbound
	err := c.do(http.MethodGet, "/api/parts/inbounds?part_id="+strconv.FormatInt(partID, 10), nil, &out)
	return out, err
}

func (c *Client) AdjustInbounds(in usecase.AdjustInboundsInput) error {
	items := make([]map[string]any, 0, len(in.Items))
	for _, it := range in.Items {
		items = append(items, map[string]any{"inbound_id": it.InboundID, "remaining": it.Remaining})
	}
	body := map[string]any{"part_id": in.PartID, "items": items}
	return c.do(http.MethodPost, "/api/parts/adjust-inbounds", body, nil)
}

func (c *Client) AdjustStock(in usecase.AdjustStockInput) error {
	body := map[string]any{"part_id": in.PartID, "new_qty": in.NewQty}
	return c.do(http.MethodPost, "/api/parts/adjust-stock", body, nil)
}

func (c *Client) AddBOMItem(b *model.BOMItem) (*model.BOMItem, error) {
	var out model.BOMItem
	if err := c.do(http.MethodPost, "/api/bom", b, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) UpdateBOMItem(b *model.BOMItem) (*model.BOMItem, error) {
	body := map[string]any{
		"id":          b.ID,
		"quantity":    b.Quantity,
		"loss_rate":   b.LossRate,
		"use_mode":    b.UseMode,
		"replaceable": b.Replaceable,
		"version":     b.Version,
	}
	var out model.BOMItem
	if err := c.do(http.MethodPut, "/api/bom", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) StockProjection(in usecase.StockProjectionInput) (*usecase.StockProjectionResult, error) {
	path := "/api/stock/projection"
	if in.ProductID > 0 && in.PlanQty > 0 {
		path += "?product_id=" + strconv.FormatInt(in.ProductID, 10) +
			"&plan_qty=" + strconv.Itoa(in.PlanQty)
	}
	var out usecase.StockProjectionResult
	if err := c.do(http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) GetBOMByProduct(productID int64) ([]model.BOMItem, error) {
	var out []model.BOMItem
	err := c.do(http.MethodGet, "/api/bom?product_id="+strconv.FormatInt(productID, 10), nil, &out)
	return out, err
}

func (c *Client) RemoveBOMItem(id int64, operator string) error {
	return c.do(http.MethodDelete, "/api/bom?id="+strconv.FormatInt(id, 10), nil, nil)
}

func (c *Client) ValidateBOM(productID int64) (bool, error) {
	var out struct {
		Valid bool `json:"valid"`
	}
	if err := c.do(http.MethodGet, "/api/bom/validate?product_id="+strconv.FormatInt(productID, 10), nil, &out); err != nil {
		return false, err
	}
	return out.Valid, nil
}

// ---- Production / Trace / Audit / Stats / Backup ----

func (c *Client) CreateBatch(b *model.ProductBatch) (*model.ProductBatch, error) {
	var out model.ProductBatch
	if err := c.do(http.MethodPost, "/api/batches", b, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) ListBatches() ([]model.ProductBatch, error) {
	var out []model.ProductBatch
	err := c.do(http.MethodGet, "/api/batches", nil, &out)
	return out, err
}

func (c *Client) UpdateBatchStatus(in usecase.UpdateBatchStatusInput) error {
	body := map[string]any{"id": in.ID, "status": in.Status}
	return c.do(http.MethodPost, "/api/batches/status", body, nil)
}

func (c *Client) RevokeBatch(in usecase.RevokeBatchInput) error {
	return c.do(http.MethodPost, "/api/batches/revoke", map[string]any{"id": in.ID}, nil)
}

func (c *Client) AddSkipPart(batchID, partID int64, operator string) error {
	return c.do(http.MethodPost, "/api/batches/skip", map[string]any{"batch_id": batchID, "part_id": partID}, nil)
}

func (c *Client) RemoveSkipPart(batchID, partID int64, operator string) error {
	path := "/api/batches/skip?batch_id=" + strconv.FormatInt(batchID, 10) + "&part_id=" + strconv.FormatInt(partID, 10)
	return c.do(http.MethodDelete, path, nil, nil)
}

func (c *Client) GetSkippedParts(batchID int64) ([]int64, error) {
	var out []int64
	err := c.do(http.MethodGet, "/api/batches/skipped?batch_id="+strconv.FormatInt(batchID, 10), nil, &out)
	return out, err
}

func (c *Client) ListTempParts(batchID int64) ([]model.BatchTempPart, error) {
	var out []model.BatchTempPart
	err := c.do(http.MethodGet, "/api/batches/temp-parts?batch_id="+strconv.FormatInt(batchID, 10), nil, &out)
	return out, err
}

func (c *Client) AddTempPart(in usecase.AddTempPartInput) error {
	body := map[string]any{"batch_id": in.BatchID, "part_id": in.PartID, "qty": in.Qty}
	if in.UseMode != nil {
		body["use_mode"] = *in.UseMode
		body["quantity"] = *in.Quantity
		body["loss_rate"] = *in.LossRate
	}
	return c.do(http.MethodPost, "/api/batches/temp-parts", body, nil)
}

func (c *Client) RemoveTempPart(batchID, partID int64, operator string) error {
	path := "/api/batches/temp-parts?batch_id=" + strconv.FormatInt(batchID, 10) + "&part_id=" + strconv.FormatInt(partID, 10)
	return c.do(http.MethodDelete, path, nil, nil)
}

func (c *Client) RecordTrace(t *model.BatchTrace) (*model.BatchTrace, error) {
	var out model.BatchTrace
	if err := c.do(http.MethodPost, "/api/traces", t, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) RecordTraces(traces []*model.BatchTrace) error {
	return c.do(http.MethodPost, "/api/traces/batch", traces, nil)
}

func (c *Client) GetTraceByBatch(batchID int64) ([]model.BatchTrace, error) {
	var out []model.BatchTrace
	err := c.do(http.MethodGet, "/api/traces/by-batch?batch_id="+strconv.FormatInt(batchID, 10), nil, &out)
	return out, err
}

func (c *Client) TraceByProduct(batchNo string, partial bool) ([]model.BatchTrace, error) {
	var out []model.BatchTrace
	err := c.do(http.MethodGet, "/api/traces/by-product?batch_no="+url.QueryEscape(batchNo)+matchQuery(partial), nil, &out)
	return out, err
}

func (c *Client) TraceByPartLot(batchNo string, partial bool) ([]model.BatchTrace, error) {
	var out []model.BatchTrace
	err := c.do(http.MethodGet, "/api/traces/by-part-lot?batch_no="+url.QueryEscape(batchNo)+matchQuery(partial), nil, &out)
	return out, err
}

func (c *Client) TraceByPart(partCode string, partial bool) ([]model.BatchTrace, error) {
	var out []model.BatchTrace
	err := c.do(http.MethodGet, "/api/traces/by-part?part_code="+url.QueryEscape(partCode)+matchQuery(partial), nil, &out)
	return out, err
}

// matchQuery 只在部分匹配时附加参数；完全匹配保持旧 URL（服务端缺省即精确）。
func matchQuery(partial bool) string {
	if partial {
		return "&match=partial"
	}
	return ""
}

func (c *Client) GetAuditLogs(tableName string, recordID int64) ([]model.AuditLog, error) {
	var out []model.AuditLog
	path := "/api/audit?table=" + url.QueryEscape(tableName) + "&record_id=" + strconv.FormatInt(recordID, 10)
	err := c.do(http.MethodGet, path, nil, &out)
	return out, err
}

func (c *Client) ListRecentAuditLogs(limit int) ([]model.AuditLog, error) {
	var out []model.AuditLog
	err := c.do(http.MethodGet, "/api/audit/recent?limit="+strconv.Itoa(limit), nil, &out)
	return out, err
}

func (c *Client) GetStockStats(days int) (*usecase.StockStats, error) {
	var out usecase.StockStats
	if err := c.do(http.MethodGet, "/api/stats?days="+strconv.Itoa(days), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) DataDir() string { return "" }

func (c *Client) SetDataDir(dir string) error {
	return fmt.Errorf("连接服务器模式不能修改本机数据目录")
}

func (c *Client) BackupDatabase(saveDir string) (string, error) {
	// 备份产物在服务器数据目录；saveDir 忽略（D：备份跟库走）。
	var out struct {
		Path string `json:"path"`
	}
	if err := c.do(http.MethodPost, "/api/backup", map[string]any{}, &out); err != nil {
		return "", err
	}
	return out.Path, nil
}

func (c *Client) RestoreDatabase(filePath string) (usecase.RestoreResult, error) {
	return usecase.RestoreResult{}, fmt.Errorf("连接服务器模式请在主机上执行恢复")
}

func (c *Client) ClearDatabase() (usecase.ClearResult, error) {
	return usecase.ClearResult{}, fmt.Errorf("连接服务器模式请在主机上执行清库")
}

func (c *Client) ExportAuditLogCSV(startDate, endDate time.Time, filePath string) (int, error) {
	body := map[string]any{"start": startDate, "end": endDate}
	// 走 /download 而不是 /api/export/audit：后者现在只回报文件**位置**（与全量导出对齐，
	// 不往「下载」里塞文件），要字节得问专门的下载端点。
	raw, err := c.doBytes(http.MethodPost, "/api/export/audit/download", body)
	if err != nil {
		return 0, err
	}
	count := 0
	if v := c.lastCount; v != "" {
		count, _ = strconv.Atoi(v)
	}
	if err := os.WriteFile(filePath, raw, 0o600); err != nil {
		return 0, err
	}
	return count, nil
}

func (c *Client) ExportAllDataCSV(saveDir string) (map[string]string, string, error) {
	var out struct {
		Files map[string]string `json:"files"`
		Dir   string            `json:"dir"`
	}
	if err := c.do(http.MethodPost, "/api/export/all", map[string]any{}, &out); err != nil {
		return nil, "", err
	}
	return out.Files, out.Dir, nil
}

// Describe 报告当前连接的服务器所使用的数据库身份（D-016）。
func (c *Client) Describe() (usecase.ServerInfo, error) {
	var out usecase.ServerInfo
	if err := c.do(http.MethodGet, "/api/v1/serverinfo", nil, &out); err != nil {
		return usecase.ServerInfo{}, err
	}
	return out, nil
}

// Probe 探测目标服务器并读取它的自描述信息（D-016）。
//
// 无需登录：切换运行模式前的预检要先问清"对面是谁、用的是哪个库"，
// 此时还没有会话。地址写法与 NewClient 一致（可带尾部斜杠）。
func Probe(baseURL string) (usecase.ServerInfo, error) {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		return usecase.ServerInfo{}, fmt.Errorf("服务器地址为空")
	}
	req, err := http.NewRequest(http.MethodGet, base+"/api/v1/serverinfo", nil)
	if err != nil {
		return usecase.ServerInfo{}, err
	}
	hc := &http.Client{Timeout: 8 * time.Second}
	resp, err := hc.Do(req)
	if err != nil {
		return usecase.ServerInfo{}, fmt.Errorf("无法连接服务器：%w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return usecase.ServerInfo{}, fmt.Errorf("服务器返回 %s", resp.Status)
	}
	var out usecase.ServerInfo
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return usecase.ServerInfo{}, fmt.Errorf("解析服务器信息失败：%w", err)
	}
	return out, nil
}
