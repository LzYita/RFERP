package api_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/jmoiron/sqlx"

	"app/internal/api"
	"app/internal/migrate"
	"app/internal/model"
	"app/internal/repository"
	"app/internal/service"
)

func strPtr(s string) *string { return &s }

// TestAPIStockInFlowIntegration 连独立开发库跑通登录→入库→审计（C5）。
// 需设置 RFERP_TEST_DSN，例如（go-sql-driver 需要 user:pass@tcp，空密码保留冒号）：
//
//	rferp_test:@tcp(127.0.0.1:33306)/rferp_dev_api?parseTime=true
//
// 使用隔离测试实例（私有 mysql-test），禁止指向业务库。该测试会清空目标库中的业务表。
func TestAPIStockInFlowIntegration(t *testing.T) {
	dsn := os.Getenv("RFERP_TEST_DSN")
	if dsn == "" {
		t.Skip("RFERP_TEST_DSN not set; skip API integration test")
	}

	db, err := sqlx.Connect("mysql", dsn)
	if err != nil {
		t.Fatalf("connect dev db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// 每次跑干净库，避免依赖外部状态。
	for _, stmt := range []string{
		"DROP TABLE IF EXISTS batch_consumptions",
		"DROP TABLE IF EXISTS batch_skip_parts",
		"DROP TABLE IF EXISTS batch_trace",
		"DROP TABLE IF EXISTS product_batches",
		"DROP TABLE IF EXISTS bom_items",
		"DROP TABLE IF EXISTS parts",
		"DROP TABLE IF EXISTS products",
		"DROP TABLE IF EXISTS audit_log",
		"DROP TABLE IF EXISTS users",
		"DROP TABLE IF EXISTS schema_migrations",
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("reset %s: %v", stmt, err)
		}
	}

	if _, err := migrate.Run(db, migrate.Options{DSN: dsn}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	apps := service.New(repository.New(db), dsn, "", nil)
	if _, err := apps.CreateInitialAdmin("apitest", "pass1234", "API Test"); err != nil {
		t.Fatalf("create admin: %v", err)
	}
	part, err := apps.CreatePart(&model.Part{Code: "P-API", Name: "API Part", Unit: "个", WarnQty: 0, Status: 1, Operator: strPtr("apitest")})
	if err != nil {
		t.Fatalf("create part: %v", err)
	}

	ts := httptest.NewServer(api.New(apps).Handler())
	t.Cleanup(ts.Close)

	// 登录
	resp, err := http.Post(ts.URL+"/api/login", "application/json",
		bytes.NewReader([]byte(`{"username":"apitest","password":"pass1234"}`)))
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d", resp.StatusCode)
	}
	var login struct {
		Token string     `json:"token"`
		User  model.User `json:"user"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&login); err != nil {
		t.Fatalf("decode login: %v", err)
	}

	// 入库（操作人应为会话用户）
	payload := fmt.Sprintf(`{"part_id":%d,"qty":3}`, part.ID)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/parts/stock-in", bytes.NewReader([]byte(payload)))
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

	got, err := apps.GetPart(part.ID)
	if err != nil || got == nil {
		t.Fatalf("get part: %v", err)
	}
	if got.StockQty != 3 {
		t.Fatalf("stock = %v, want 3", got.StockQty)
	}

	logs, err := apps.ListRecentAuditLogs(20)
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	found := false
	for _, lg := range logs {
		if lg.Action == "STOCK_IN" && lg.TableName == "parts" && lg.RecordID == part.ID {
			found = true
			if lg.Operator == nil || *lg.Operator == "" {
				t.Fatalf("STOCK_IN audit missing operator")
			}
			break
		}
	}
	if !found {
		t.Fatalf("STOCK_IN audit not found; logs=%d", len(logs))
	}
}
