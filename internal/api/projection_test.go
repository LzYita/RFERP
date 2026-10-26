package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"app/internal/usecase"
)

type projectionApps struct {
	sessionApps
	got *usecase.StockProjectionInput
	err error
}

func (a *projectionApps) StockProjection(in usecase.StockProjectionInput) (*usecase.StockProjectionResult, error) {
	a.got = &in
	if a.err != nil {
		return nil, a.err
	}
	res := &usecase.StockProjectionResult{
		Parts: []usecase.PartProjection{{
			PartID: 7, Stock: 40, Warn: 120, Committed: 120, After: -80,
			Holders: []usecase.ProjectionHolder{{BatchNo: "B-1", Qty: 60}, {BatchNo: "B-2", Qty: 60}},
		}},
	}
	res.Summary.WillShort = 1
	return res, nil
}

func TestStockProjectionReturnsAggregates(t *testing.T) {
	apps := &projectionApps{}
	s := New(apps)
	w := postJSON(t, s, "GET", "/api/stock/projection", issueAdmin(t, s), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("状态码 = %d：%s", w.Code, w.Body.String())
	}
	var got usecase.StockProjectionResult
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Parts) != 1 || got.Parts[0].After != -80 || len(got.Parts[0].Holders) != 2 {
		t.Fatalf("响应 = %+v", got)
	}
	if got.Summary.WillShort != 1 {
		t.Fatalf("summary = %+v", got.Summary)
	}
	if apps.got == nil || apps.got.ProductID != 0 || apps.got.PlanQty != 0 {
		t.Fatalf("无参数时不应把假设批次传下去：%+v", apps.got)
	}
}

func TestStockProjectionPassesPlannedBatch(t *testing.T) {
	apps := &projectionApps{}
	s := New(apps)
	w := postJSON(t, s, "GET", "/api/stock/projection?product_id=3&plan_qty=25", issueAdmin(t, s), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("状态码 = %d：%s", w.Code, w.Body.String())
	}
	if apps.got == nil || apps.got.ProductID != 3 || apps.got.PlanQty != 25 {
		t.Fatalf("参数没转交：%+v", apps.got)
	}
}

// 只给一半参数必须报错：静默忽略会让建批预览少算一批，比 400 更危险。
func TestStockProjectionRejectsHalfPair(t *testing.T) {
	s := New(&projectionApps{})
	for _, path := range []string{
		"/api/stock/projection?product_id=3",
		"/api/stock/projection?plan_qty=25",
		"/api/stock/projection?product_id=0&plan_qty=25",
		"/api/stock/projection?product_id=3&plan_qty=0",
		"/api/stock/projection?product_id=x&plan_qty=25",
	} {
		if code := postJSON(t, s, "GET", path, issueAdmin(t, s), nil).Code; code != http.StatusBadRequest {
			t.Fatalf("%s 状态码 = %d，期望 400", path, code)
		}
	}
}

func TestStockProjectionSurfacesServiceError(t *testing.T) {
	s := New(&projectionApps{err: errBOM("投影计算失败")})
	if code := postJSON(t, s, "GET", "/api/stock/projection", issueAdmin(t, s), nil).Code; code != http.StatusInternalServerError {
		t.Fatalf("状态码 = %d，期望 500", code)
	}
}
