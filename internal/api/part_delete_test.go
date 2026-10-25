package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"app/internal/model"
	"app/internal/usecase"
)

type partDeleteApps struct {
	sessionApps
	err error
}

func (a *partDeleteApps) DeletePart(int64, string) error { return a.err }

// 被 BOM 引用时回 409 + products：界面靠这个数组列出「被哪些产品用了」，
// 所以字段名与 409 本身都是契约，不只是文案。
func TestDeletePartConflictListsBOMProducts(t *testing.T) {
	apps := &partDeleteApps{err: &usecase.PartInBOMError{
		PartCode: "M-001",
		Products: []model.ProductRef{
			{ID: 3, Code: "P-001", Name: "智能电水壶"},
			{ID: 4, Code: "P-003", Name: "空气炸锅"},
		},
	}}
	s := New(apps)
	w := postJSON(t, s, "DELETE", "/api/parts?id=7", issueAdmin(t, s), nil)

	if w.Code != http.StatusConflict {
		t.Fatalf("状态码 = %d，期望 409：%s", w.Code, w.Body.String())
	}
	var got struct {
		Error    string             `json:"error"`
		Products []model.ProductRef `json:"products"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Products) != 2 || got.Products[0].Code != "P-001" || got.Products[1].Name != "空气炸锅" {
		t.Fatalf("products = %+v", got.Products)
	}
	if got.Error == "" || got.Error == "FOREIGN KEY constraint failed" {
		t.Fatalf("error 文案没有说清原因：%q", got.Error)
	}
}

// 其它删除失败仍然是 400，且不带 products 字段（避免界面把普通错误当引用提示）。
func TestDeletePartOtherErrorsStayBadRequest(t *testing.T) {
	apps := &partDeleteApps{err: fmt.Errorf("part not found")}
	s := New(apps)
	w := postJSON(t, s, "DELETE", "/api/parts?id=9", issueAdmin(t, s), nil)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("状态码 = %d，期望 400", w.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if _, ok := body["products"]; ok {
		t.Fatalf("普通错误不应带 products：%v", body)
	}
}

// 追溯记录引用：同一个 409，但载荷是 batches + records，不是 products。
// 界面靠这个区别给出不同建议（BOM 去删条目；追溯记录只能停用零件）。
func TestDeletePartConflictListsProductionBatches(t *testing.T) {
	apps := &partDeleteApps{err: &usecase.PartInTraceError{
		PartCode: "M-009",
		Batches: []model.PartTraceRef{
			{BatchNo: "B-001", Records: 2},
			{BatchNo: "B-002", Records: 1},
		},
	}}
	s := New(apps)
	w := postJSON(t, s, "DELETE", "/api/parts?id=7", issueAdmin(t, s), nil)

	if w.Code != http.StatusConflict {
		t.Fatalf("状态码 = %d，期望 409：%s", w.Code, w.Body.String())
	}
	var got struct {
		Error   string               `json:"error"`
		Batches []model.PartTraceRef `json:"batches"`
		Records int                  `json:"records"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Records != 3 || len(got.Batches) != 2 || got.Batches[0].BatchNo != "B-001" {
		t.Fatalf("batches/records = %+v / %d", got.Batches, got.Records)
	}
	if _, ok := jsonObject(t, w.Body.Bytes())["products"]; ok {
		t.Fatalf("追溯记录的错误不应带 products")
	}
	if got.Error == "" || got.Error == "constraint failed: FOREIGN KEY constraint failed (787)" {
		t.Fatalf("error 文案没有说清原因：%q", got.Error)
	}
}

func jsonObject(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}
