package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"app/internal/model"
)

type bomUpdateApps struct {
	sessionApps
	got *model.BOMItem
	err error
}

func (a *bomUpdateApps) UpdateBOMItem(b *model.BOMItem) (*model.BOMItem, error) {
	a.got = b
	if a.err != nil {
		return nil, a.err
	}
	out := *b
	out.Version = b.Version + 1
	return &out, nil
}

// 编辑一条 BOM 明细：请求体只有三个业务字段，服务端原样转交。
func TestUpdateBOMItemPassesThreeFields(t *testing.T) {
	apps := &bomUpdateApps{}
	s := New(apps)
	w := postJSON(t, s, "PUT", "/api/bom", issueAdmin(t, s), map[string]any{
		"id": 11, "quantity": 7, "loss_rate": 2.5, "use_mode": 1, "version": 4,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("状态码 = %d：%s", w.Code, w.Body.String())
	}
	if apps.got == nil {
		t.Fatal("服务层没被调用")
	}
	if apps.got.ID != 11 || apps.got.Quantity != 7 || apps.got.LossRate != 2.5 || apps.got.UseMode != 1 || apps.got.Version != 4 {
		t.Fatalf("转交的参数不对：%+v", apps.got)
	}
	if apps.got.Operator == nil || *apps.got.Operator == "" {
		t.Fatal("没有记录操作人")
	}
	var out model.BOMItem
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Version != 5 {
		t.Fatalf("返回的 version = %d，期望 5", out.Version)
	}
}

// 多传 product_id/part_id 必须 400，而不是静默忽略：
// 否则客户端以为换了零件，服务端其实没换，界面上看是对的、库是旧的。
func TestUpdateBOMItemRejectsFieldsOutsideTheEditScope(t *testing.T) {
	apps := &bomUpdateApps{}
	s := New(apps)
	for _, field := range []string{"part_id", "product_id", "remark", "replaceable"} {
		w := postJSON(t, s, "PUT", "/api/bom", issueAdmin(t, s), map[string]any{
			"id": 11, "quantity": 1, "loss_rate": 0, "use_mode": 0, "version": 1, field: 1,
		})
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s 应被拒绝，状态码 = %d：%s", field, w.Code, w.Body.String())
		}
	}
	if apps.got != nil {
		t.Fatal("非法请求不应到达服务层")
	}
}

func TestUpdateBOMItemRequiresID(t *testing.T) {
	apps := &bomUpdateApps{}
	s := New(apps)
	w := postJSON(t, s, "PUT", "/api/bom", issueAdmin(t, s), map[string]any{
		"quantity": 1, "loss_rate": 0, "use_mode": 0, "version": 1,
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("缺 id 应 400，实际 %d", w.Code)
	}
}

// 服务层的校验错误原样回给界面（400），不要把校验失败说成 500。
func TestUpdateBOMItemSurfacesValidationError(t *testing.T) {
	apps := &bomUpdateApps{err: errBOM("BOM用量必须是大于0的有限数值")}
	s := New(apps)
	w := postJSON(t, s, "PUT", "/api/bom", issueAdmin(t, s), map[string]any{
		"id": 11, "quantity": 0, "loss_rate": 0, "use_mode": 0, "version": 1,
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("状态码 = %d：%s", w.Code, w.Body.String())
	}
	var got struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Error != "BOM用量必须是大于0的有限数值" {
		t.Fatalf("error = %q", got.Error)
	}
}

type errBOM string

func (e errBOM) Error() string { return string(e) }
