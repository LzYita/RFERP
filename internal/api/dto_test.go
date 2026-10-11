package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"app/internal/auth"
	"app/internal/model"
)

// partsApps 是零件写路径用的用例桩。
//
// 关键：UpdatePart 里会写 `*p.Operator`。Operator 为 nil 时那是空指针解引用，
// 这正是 handleUpdatePart 过去漏设 Operator 造成的崩溃。这里如实模拟，
// 用来证明修复前的行为确实是 panic，而不是替实现擦屁股。
type partsApps struct {
	sessionApps
	created []*model.Part
	updated []*model.Part
	nextID  int64
	store   map[string]int // code -> status
}

func newPartsApps() *partsApps {
	return &partsApps{nextID: 1, store: map[string]int{}}
}

func (a *partsApps) CreatePart(p *model.Part) (*model.Part, error) {
	p.ID = a.nextID
	a.nextID++
	a.created = append(a.created, p)
	if p.Status != 0 {
		a.store[p.Code] = p.Status
	}
	return p, nil
}

func (a *partsApps) UpdatePart(p *model.Part) (*model.Part, error) {
	if p.Operator == nil {
		// 与 service.UpdatePart 同样解引用：nil 会 panic。
		// 这里显式 panic，让测试能抓到「Operator 没被填」这个 bug。
		panic("nil Operator dereference")
	}
	if p.Status != 0 {
		a.store[p.Code] = p.Status
	}
	a.updated = append(a.updated, p)
	return p, nil
}

func (a *partsApps) GetPart(id int64) (*model.Part, error) {
	return &model.Part{ID: id, Code: "P-1", Status: statusDisabled}, nil
}

func (a *partsApps) CreateProduct(p *model.Product) (*model.Product, error) { return p, nil }
func (a *partsApps) UpdateProduct(p *model.Product) (*model.Product, error) { return p, nil }

func postJSON(t *testing.T, s *Server, method, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	r := httptest.NewRequest(method, path, &buf)
	r.Header.Set("Content-Type", "application/json")
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}

// issueAdmin 建一个管理员会话，权限足够写零件与产品。
func issueAdmin(t *testing.T, s *Server) string {
	t.Helper()
	tok, err := s.issueSession(&model.User{ID: 1, Username: "admin", Role: string(auth.RoleAdmin), Status: 1})
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

// 建零件时漏传 status，必须建成「启用」而不是 0（停用）。
//
// 这就是迁移中发现的问题：Fyne 侧一直显式传 1，新 Web 客户端一旦漏传，
// 零件会被静默建成停用件，且不报任何错。
func TestCreatePartDefaultsToEnabledWhenStatusOmitted(t *testing.T) {
	apps := newPartsApps()
	s := New(apps, WithStartupToken(""))
	tok := issueAdmin(t, s)

	w := postJSON(t, s, "POST", "/api/parts", tok, map[string]any{
		"code": "P-9001", "name": "新零件", "unit": "个", "stock_qty": 10, "warn_qty": 2,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("创建 = %d, body=%s", w.Code, w.Body.String())
	}
	if len(apps.created) != 1 {
		t.Fatalf("未走到 CreatePart")
	}
	if got := apps.created[0].Status; got != statusEnabled {
		t.Errorf("漏传 status 时建成 status=%d，期望 %d（启用）", got, statusEnabled)
	}
}

// 显式传停用必须被尊重——这正是选方案②要保住的能力。
func TestCreatePartHonoursExplicitDisabled(t *testing.T) {
	apps := newPartsApps()
	s := New(apps, WithStartupToken(""))
	tok := issueAdmin(t, s)

	w := postJSON(t, s, "POST", "/api/parts", tok, map[string]any{
		"code": "P-9002", "name": "停用件", "unit": "个", "stock_qty": 1, "warn_qty": 1,
		"status": statusDisabled,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("创建 = %d", w.Code)
	}
	if got := apps.created[0].Status; got != statusDisabled {
		t.Errorf("显式停用被改成 %d，期望 %d", got, statusDisabled)
	}
}

// 非法 status 必须被拒绝，而不是悄悄当成停用。
func TestCreatePartRejectsInvalidStatus(t *testing.T) {
	apps := newPartsApps()
	s := New(apps, WithStartupToken(""))
	tok := issueAdmin(t, s)

	for _, bad := range []int{0, 3, 99, -1} {
		w := postJSON(t, s, "POST", "/api/parts", tok, map[string]any{
			"code": "P-9003", "name": "x", "unit": "个", "status": bad,
		})
		if w.Code != http.StatusBadRequest {
			t.Errorf("status=%d 返回 %d，期望 400", bad, w.Code)
		}
	}
}

// 更新时漏传 status 必须沿用原值，不能静默改成停用。
//
// 用户只是改了名字，零件却消失了，是最坏的解读。
func TestUpdatePartKeepsStatusWhenOmitted(t *testing.T) {
	apps := newPartsApps()
	s := New(apps, WithStartupToken(""))
	tok := issueAdmin(t, s)

	w := postJSON(t, s, "PUT", "/api/parts", tok, map[string]any{
		"id": 1, "code": "P-1", "name": "改名了", "unit": "个", "version": 1,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("更新 = %d, body=%s", w.Code, w.Body.String())
	}
	if len(apps.updated) != 1 {
		t.Fatal("未走到 UpdatePart")
	}
	// GetPart 桩返回 statusDisabled：更新后应保持停用，而不是被改成启用或 0。
	if got := apps.updated[0].Status; got != statusDisabled {
		t.Errorf("更新时漏传 status 得到 %d，期望沿用原值 %d", got, statusDisabled)
	}
}

// 回归：handleUpdatePart 过去没有设置 Operator，而 service.UpdatePart 会解引用它，
// 走 HTTP 更新零件必然空指针 panic。这里显式证明 Operator 被填上了。
func TestUpdatePartSetsOperator(t *testing.T) {
	apps := newPartsApps()
	s := New(apps, WithStartupToken(""))
	tok := issueAdmin(t, s)

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("更新零件触发 panic: %v；Operator 未被填充", r)
		}
	}()

	w := postJSON(t, s, "PUT", "/api/parts", tok, map[string]any{
		"id": 1, "code": "P-1", "name": "n", "unit": "个", "status": 1, "version": 1,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("更新 = %d", w.Code)
	}
	if apps.updated[0].Operator == nil {
		t.Fatal("Operator 为 nil")
	}
}

// 产品走同一套规则：漏传即启用。
func TestCreateProductDefaultsToEnabledWhenStatusOmitted(t *testing.T) {
	apps := newPartsApps()
	s := New(apps, WithStartupToken(""))
	tok := issueAdmin(t, s)

	w := postJSON(t, s, "POST", "/api/products", tok, map[string]any{
		"code": "PR-1", "name": "新产品", "unit": "台",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("创建产品 = %d, body=%s", w.Code, w.Body.String())
	}
}

// 多余字段应当被拒绝：拼错字段名会静默丢失，是接口演进时最常见的坑。
func TestUnknownFieldRejected(t *testing.T) {
	apps := newPartsApps()
	s := New(apps, WithStartupToken(""))
	tok := issueAdmin(t, s)

	w := postJSON(t, s, "POST", "/api/parts", tok, map[string]any{
		"code": "P-9", "name": "n", "unit": "个", "stok_qty": 5, // 拼错
	})
	if w.Code != http.StatusBadRequest {
		t.Errorf("含未知字段返回 %d，期望 400（拼错的字段会静默丢失）", w.Code)
	}
}
