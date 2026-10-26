package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"app/internal/model"
)

type tempPartApps struct {
	sessionApps
	added    []float64 // 记录每次 AddTempPart 的 qty，验证参数转交
	addErr   error
	listRows []model.BatchTempPart
}

func (a *tempPartApps) ListTempParts(batchID int64) ([]model.BatchTempPart, error) {
	return a.listRows, nil
}

func (a *tempPartApps) AddTempPart(batchID, partID int64, qty float64) error {
	a.added = append(a.added, qty)
	return a.addErr
}

func (a *tempPartApps) RemoveTempPart(batchID, partID int64) error { return nil }

// 端到端只验证路由/解码/权限接线；库存检测与扣减语义在 service 层已用真实
// SQLite 钉住（temppart_test.go），这里不打桩复述。
func TestTempPartsEndpointsRoundTrip(t *testing.T) {
	apps := &tempPartApps{listRows: []model.BatchTempPart{{
		BatchID: 5, PartID: 7, Qty: 12.5, Code: "M-1", Name: "临时件", Unit: "个",
	}}}
	s := New(apps)
	auth := issueAdmin(t, s)

	w := postJSON(t, s, "GET", "/api/batches/temp-parts?batch_id=5", auth, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET 状态码 = %d：%s", w.Code, w.Body.String())
	}
	var rows []model.BatchTempPart
	if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Qty != 12.5 || rows[0].Code != "M-1" {
		t.Fatalf("GET 响应 = %+v", rows)
	}

	w = postJSON(t, s, "POST", "/api/batches/temp-parts", auth, map[string]any{"batch_id": 5, "part_id": 7, "qty": 12.5})
	if w.Code != http.StatusOK {
		t.Fatalf("POST 状态码 = %d：%s", w.Code, w.Body.String())
	}
	if len(apps.added) != 1 || apps.added[0] != 12.5 {
		t.Fatalf("AddTempPart 参数没转交：%+v", apps.added)
	}

	w = postJSON(t, s, "DELETE", "/api/batches/temp-parts?batch_id=5&part_id=7", auth, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("DELETE 状态码 = %d：%s", w.Code, w.Body.String())
	}

	// 多余字段必须被拒（DisallowUnknownFields），静默忽略会把错数量当对的扣。
	w = postJSON(t, s, "POST", "/api/batches/temp-parts", auth, map[string]any{"batch_id": 5, "part_id": 7, "qty": 1, "note": "x"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("未知字段状态码 = %d，期望 400", w.Code)
	}
}

// 服务端拒绝（库存不足等）原样带出错误文案，前端只负责展示。
func TestTempPartAddSurfacesServiceError(t *testing.T) {
	s := New(&tempPartApps{addErr: errors.New("临时需要 20.00，全部在产批次完成后将缺 10.00")})
	w := postJSON(t, s, "POST", "/api/batches/temp-parts", issueAdmin(t, s),
		map[string]any{"batch_id": 5, "part_id": 7, "qty": 20})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("状态码 = %d，期望 400", w.Code)
	}
	if body := w.Body.String(); body == "" || body == "{}" {
		t.Fatal("错误文案丢失")
	}
}
