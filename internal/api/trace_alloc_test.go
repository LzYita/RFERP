package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"app/internal/model"
	"app/internal/usecase"
)

// 投料记录的批次分配要能原样穿过 HTTP 层：前端按 inbound_id 提交顺序，
// 服务端按数组顺序落 position；读回来时带着 batch_no 供界面显示。
type traceAllocApps struct {
	sessionApps
	saved []*model.BatchTrace
	read  []model.BatchTrace
}

func (a *traceAllocApps) RecordTraces(traces []*model.BatchTrace) error {
	a.saved = traces
	return nil
}

func (a *traceAllocApps) GetTraceByBatch(batchID int64) ([]model.BatchTrace, error) {
	return a.read, nil
}

func TestRecordTracesPassesBatchAllocations(t *testing.T) {
	apps := &traceAllocApps{}
	s := New(apps)
	body := []map[string]any{{
		"batch_id": 3, "part_id": 7, "used_qty": 10, "supplier": "甲供",
		"allocations": []map[string]any{{"inbound_id": 11, "qty": 6}, {"inbound_id": 12, "qty": 4}},
	}}
	w := postJSON(t, s, "POST", "/api/traces/batch", issueAdmin(t, s), body)
	if w.Code != http.StatusOK {
		t.Fatalf("状态码 = %d：%s", w.Code, w.Body.String())
	}
	if len(apps.saved) != 1 {
		t.Fatalf("落库行数 = %d，want 1", len(apps.saved))
	}
	got := apps.saved[0].Allocations
	if len(got) != 2 || got[0].InboundID != 11 || got[1].InboundID != 12 {
		t.Fatalf("分配没有按顺序穿过 HTTP 层：%+v", got)
	}
	if got[0].Qty != 6 || apps.saved[0].UsedQty != 10 {
		t.Fatalf("数量 = %+v / used=%v", got, apps.saved[0].UsedQty)
	}
}

func TestGetTraceByBatchReturnsAllocations(t *testing.T) {
	apps := &traceAllocApps{read: []model.BatchTrace{{
		ID: 1, BatchID: 3, PartID: 7, UsedQty: 10,
		Allocations: []model.BatchTraceAlloc{{InboundID: 11, BatchNo: "RK-20260101-0011", Qty: 6, Position: 0}},
	}}}
	s := New(apps)
	w := postJSON(t, s, "GET", "/api/traces/by-batch?batch_id=3", issueAdmin(t, s), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("状态码 = %d：%s", w.Code, w.Body.String())
	}
	var out []struct {
		UsedQty     float64 `json:"used_qty"`
		Allocations []struct {
			InboundID int64   `json:"inbound_id"`
			BatchNo   string  `json:"batch_no"`
			Qty       float64 `json:"qty"`
		} `json:"allocations"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || len(out[0].Allocations) != 1 {
		t.Fatalf("响应 = %+v", out)
	}
	a := out[0].Allocations[0]
	if a.InboundID != 11 || a.BatchNo != "RK-20260101-0011" || a.Qty != 6 {
		t.Fatalf("分配字段 = %+v", a)
	}
}

var _ usecase.Trace = (*traceAllocApps)(nil)
