package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"app/internal/model"
	"app/internal/usecase"
)

type fakeApps struct {
	usecase.Applications // 未实现的用例置 nil，测试只触达下列方法

	loginErr   error
	loginUser  *model.User
	parts      []model.Part
	stockIn    usecase.StockInInput
	stockErr   error
	inbounds   []model.PartInbound
	inboundID  int64
	inboundErr error
	userCount  int
}

func testWarehouseUser() *model.User {
	name := "仓管甲"
	return &model.User{ID: 7, Username: "wh", Role: "warehouse", DisplayName: &name}
}

func testViewerUser() *model.User {
	return &model.User{ID: 1, Username: "v", Role: "viewer"}
}

func stockInput() usecase.StockInInput {
	return usecase.StockInInput{PartID: 20, Qty: 2, Operator: "ignored-by-api"}
}

func (f *fakeApps) Login(username, password string) (*model.User, error) {
	if f.loginErr != nil {
		return nil, f.loginErr
	}
	return f.loginUser, nil
}

func (f *fakeApps) ListParts() ([]model.Part, error) { return f.parts, nil }

func (f *fakeApps) StockIn(in usecase.StockInInput) (string, error) {
	f.stockIn = in
	return "RK-20260101-0001", f.stockErr
}

func (f *fakeApps) ListPartInbounds(partID int64) ([]model.PartInbound, error) {
	f.inboundID = partID
	return f.inbounds, f.inboundErr
}

func (f *fakeApps) ListBatches() ([]model.ProductBatch, error) { return nil, nil }

func (f *fakeApps) ListRecentAuditLogs(limit int) ([]model.AuditLog, error) { return nil, nil }

func (f *fakeApps) UserCount() (int, error) { return f.userCount, nil }

func (f *fakeApps) CreateInitialAdmin(username, password, displayName string) (*model.User, error) {
	name := displayName
	return &model.User{ID: 9, Username: username, Role: "admin", DisplayName: &name}, nil
}

func TestLoginAndStockInUsesSessionOperator(t *testing.T) {
	name := "仓管甲"
	apps := &fakeApps{loginUser: &model.User{ID: 7, Username: "wh", Role: "warehouse", DisplayName: &name}}
	s := New(apps)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/login", "application/json", bytes.NewReader([]byte(`{"username":"wh","password":"pw"}`)))
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d", resp.StatusCode)
	}
	var login struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&login); err != nil {
		t.Fatalf("decode login: %v", err)
	}

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/parts/stock-in", bytes.NewReader([]byte(`{"part_id":20,"qty":2}`)))
	req.Header.Set("Authorization", "Bearer "+login.Token)
	req.Header.Set("Content-Type", "application/json")
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("stock-in: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("stock-in status = %d", resp2.StatusCode)
	}
	if apps.stockIn.Operator != name {
		t.Fatalf("operator = %q, want session display name %q", apps.stockIn.Operator, name)
	}
	if apps.stockIn.PartID != 20 || apps.stockIn.Qty != 2 {
		t.Fatalf("stock-in input = %+v", apps.stockIn)
	}
}

func TestStockInRequiresPermission(t *testing.T) {
	apps := &fakeApps{loginUser: &model.User{ID: 1, Username: "v", Role: "viewer"}}
	s := New(apps)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/login", "application/json", bytes.NewReader([]byte(`{"username":"v","password":"pw"}`)))
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	defer resp.Body.Close()
	var login struct {
		Token string `json:"token"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&login)

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/parts/stock-in", bytes.NewReader([]byte(`{"part_id":1,"qty":1}`)))
	req.Header.Set("Authorization", "Bearer "+login.Token)
	req.Header.Set("Content-Type", "application/json")
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("stock-in: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusForbidden {
		t.Fatalf("viewer stock-in status = %d, want 403", resp2.StatusCode)
	}
}

func TestHealthAndUnauthorized(t *testing.T) {
	s := New(&fakeApps{})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d", resp.StatusCode)
	}

	resp2, err := http.Get(ts.URL + "/api/parts")
	if err != nil {
		t.Fatalf("parts: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d", resp2.StatusCode)
	}
}

func TestStockInReturnsBatchNo(t *testing.T) {
	apps := &fakeApps{loginUser: testWarehouseUser()}
	s := New(apps)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	c := NewClient(ts.URL)
	if _, err := c.Login("wh", "pw"); err != nil {
		t.Fatalf("login: %v", err)
	}
	bn, err := c.StockIn(usecase.StockInInput{PartID: 20, Qty: 2, Note: "补货"})
	if err != nil || bn != "RK-20260101-0001" {
		t.Fatalf("stock in: bn=%q err=%v", bn, err)
	}
	if apps.stockIn.Note != "补货" {
		t.Fatalf("note not passed through: %+v", apps.stockIn)
	}
}

func TestListPartInboundsValidatesPartID(t *testing.T) {
	apps := &fakeApps{
		loginUser: testWarehouseUser(),
		inbounds:  []model.PartInbound{{ID: 1, PartID: 20, BatchNo: "RK-20260101-0001", Qty: 2}},
	}
	s := New(apps)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	c := NewClient(ts.URL)
	if _, err := c.Login("wh", "pw"); err != nil {
		t.Fatalf("login: %v", err)
	}
	rows, err := c.ListPartInbounds(20)
	if err != nil || len(rows) != 1 || rows[0].BatchNo != "RK-20260101-0001" {
		t.Fatalf("rows=%v err=%v", rows, err)
	}
	if apps.inboundID != 20 {
		t.Fatalf("part id = %d", apps.inboundID)
	}
	// 缺 part_id 是调用方拼错 URL，必须报错而不是返回空列表。
	if _, err := c.ListPartInbounds(0); err == nil {
		t.Fatal("missing part_id must fail")
	}
}
