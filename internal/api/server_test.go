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

	loginErr  error
	loginUser *model.User
	parts     []model.Part
	stockIn   usecase.StockInInput
	stockErr  error
}

func (f *fakeApps) Login(username, password string) (*model.User, error) {
	if f.loginErr != nil {
		return nil, f.loginErr
	}
	return f.loginUser, nil
}

func (f *fakeApps) ListParts() ([]model.Part, error) { return f.parts, nil }

func (f *fakeApps) StockIn(in usecase.StockInInput) error {
	f.stockIn = in
	return f.stockErr
}

func (f *fakeApps) ListBatches() ([]model.ProductBatch, error) { return nil, nil }

func (f *fakeApps) ListRecentAuditLogs(limit int) ([]model.AuditLog, error) { return nil, nil }

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
