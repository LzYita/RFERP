package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"app/internal/model"
	"app/internal/usecase"
)

// Client 是远程用例适配（D1/D2）：UI 经 HTTP 访问主机上的同一 Service。
type Client struct {
	base string
	hc   *http.Client
	tok  string
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
func (c *Client) SetToken(tok string) { c.tok = tok }

// Token 返回当前会话。
func (c *Client) Token() string { return c.tok }

func (c *Client) do(method, path string, in any, out any) error {
	var body io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, c.base+path, body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.tok != "" {
		req.Header.Set("Authorization", "Bearer "+c.tok)
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
		var er struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(raw, &er)
		if er.Error == "" {
			er.Error = strings.TrimSpace(string(raw))
		}
		if er.Error == "" {
			er.Error = http.StatusText(resp.StatusCode)
		}
		return &APIError{Status: resp.StatusCode, Message: er.Error}
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, out)
}

// APIError 保留 HTTP 状态，便于 UI/上层判断。
type APIError struct {
	Status  int
	Message string
}

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
	c.tok = ""
	var out struct {
		Token string     `json:"token"`
		User  model.User `json:"user"`
	}
	in := map[string]string{"username": username, "password": password}
	if err := c.do(http.MethodPost, "/api/login", in, &out); err != nil {
		return nil, err
	}
	c.tok = out.Token
	u := out.User
	return &u, nil
}

func (c *Client) CreateInitialAdmin(username, password, displayName string) (*model.User, error) {
	return c.CreateUser(usecase.CreateUserInput{
		Username: username, Password: password, DisplayName: displayName, Role: "admin",
	})
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
	list, err := c.ListProducts()
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].ID == id {
			return &list[i], nil
		}
	}
	return nil, fmt.Errorf("product not found")
}

func (c *Client) ListProducts() ([]model.Product, error) {
	var out []model.Product
	err := c.do(http.MethodGet, "/api/products", nil, &out)
	return out, err
}

func (c *Client) UpdateProduct(p *model.Product) (*model.Product, error) {
	return nil, fmt.Errorf("not implemented in remote client yet")
}

func (c *Client) DeleteProduct(id int64, operator string) error {
	return fmt.Errorf("not implemented in remote client yet")
}

func (c *Client) CreatePart(p *model.Part) (*model.Part, error) {
	return nil, fmt.Errorf("not implemented in remote client yet")
}

func (c *Client) GetPart(id int64) (*model.Part, error) {
	parts, err := c.ListParts()
	if err != nil {
		return nil, err
	}
	for i := range parts {
		if parts[i].ID == id {
			return &parts[i], nil
		}
	}
	return nil, fmt.Errorf("part not found")
}

func (c *Client) ListParts() ([]model.Part, error) {
	var out []model.Part
	err := c.do(http.MethodGet, "/api/parts", nil, &out)
	return out, err
}

func (c *Client) UpdatePart(p *model.Part) (*model.Part, error) {
	return nil, fmt.Errorf("not implemented in remote client yet")
}

func (c *Client) DeletePart(id int64, operator string) error {
	return fmt.Errorf("not implemented in remote client yet")
}

func (c *Client) StockIn(in usecase.StockInInput) error {
	// operator 由服务端会话注入；此处字段仅为兼容签名。
	body := map[string]any{"part_id": in.PartID, "qty": in.Qty}
	return c.do(http.MethodPost, "/api/parts/stock-in", body, nil)
}

func (c *Client) AdjustStock(in usecase.AdjustStockInput) error {
	body := map[string]any{"part_id": in.PartID, "new_qty": in.NewQty}
	return c.do(http.MethodPost, "/api/parts/adjust-stock", body, nil)
}

func (c *Client) AddBOMItem(b *model.BOMItem) (*model.BOMItem, error) {
	return nil, fmt.Errorf("not implemented in remote client yet")
}

func (c *Client) GetBOMByProduct(productID int64) ([]model.BOMItem, error) {
	return nil, fmt.Errorf("not implemented in remote client yet")
}

func (c *Client) RemoveBOMItem(id int64, operator string) error {
	return fmt.Errorf("not implemented in remote client yet")
}

func (c *Client) ValidateBOM(productID int64) (bool, error) {
	return false, fmt.Errorf("not implemented in remote client yet")
}

// ---- Production / Trace / Audit / Stats / Backup ----

func (c *Client) CreateBatch(b *model.ProductBatch) (*model.ProductBatch, error) {
	return nil, fmt.Errorf("not implemented in remote client yet")
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
	return fmt.Errorf("not implemented in remote client yet")
}

func (c *Client) AddSkipPart(batchID, partID int64) error {
	return fmt.Errorf("not implemented in remote client yet")
}

func (c *Client) RemoveSkipPart(batchID, partID int64) error {
	return fmt.Errorf("not implemented in remote client yet")
}

func (c *Client) GetSkippedParts(batchID int64) ([]int64, error) {
	return nil, fmt.Errorf("not implemented in remote client yet")
}

func (c *Client) RecordTrace(t *model.BatchTrace) (*model.BatchTrace, error) {
	return nil, fmt.Errorf("not implemented in remote client yet")
}

func (c *Client) RecordTraces(traces []*model.BatchTrace) error {
	return fmt.Errorf("not implemented in remote client yet")
}

func (c *Client) GetTraceByBatch(batchID int64) ([]model.BatchTrace, error) {
	return nil, fmt.Errorf("not implemented in remote client yet")
}

func (c *Client) TraceByProduct(batchNo string) ([]model.BatchTrace, error) {
	return nil, fmt.Errorf("not implemented in remote client yet")
}

func (c *Client) TraceByPart(partCode string) ([]model.BatchTrace, error) {
	return nil, fmt.Errorf("not implemented in remote client yet")
}

func (c *Client) GetAuditLogs(tableName string, recordID int64) ([]model.AuditLog, error) {
	return nil, fmt.Errorf("not implemented in remote client yet")
}

func (c *Client) ListRecentAuditLogs(limit int) ([]model.AuditLog, error) {
	var out []model.AuditLog
	err := c.do(http.MethodGet, "/api/audit/recent?limit="+strconv.Itoa(limit), nil, &out)
	return out, err
}

func (c *Client) GetStockStats(days int) (*usecase.StockStats, error) {
	return nil, fmt.Errorf("not implemented in remote client yet")
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

func (c *Client) RestoreDatabase(filePath string) (int, int, error) {
	return 0, 0, fmt.Errorf("连接服务器模式请在主机上执行恢复")
}

func (c *Client) ClearDatabase() error {
	return fmt.Errorf("连接服务器模式请在主机上执行清库")
}

func (c *Client) ExportAuditLogCSV(startDate, endDate time.Time, filePath string) (int, error) {
	return 0, fmt.Errorf("not implemented in remote client yet")
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
