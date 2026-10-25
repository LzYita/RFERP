package service

import (
	"strings"
	"testing"

	"app/internal/model"
	"app/internal/repository"
)

// BOM 明细的编辑只允许改用量、用量模式与损耗率。
//
// 这条约束在服务层再确认一次（仓储层已经只写三个字段）：把"编辑数量"
// 与"换零件"分开，是这两个动作在审计日志里的区别能说清的前提。
type bomUpdateStore struct {
	repository.Store
	old     *model.BOMItem
	updated *model.BOMItem
	audit   *model.AuditLog
}

func (s *bomUpdateStore) WithTx(fn func(repository.TxOps) error) error {
	return fn(&bomUpdateTx{store: s})
}

type bomUpdateTx struct {
	repository.TxOps
	store *bomUpdateStore
}

func (t *bomUpdateTx) GetBOMItemForUpdate(int64) (*model.BOMItem, error) {
	return t.store.old, nil
}

// 与真仓储同一语义：版本不符合时 0 行受影响。
func (t *bomUpdateTx) UpdateBOMItem(b *model.BOMItem) (int64, error) {
	if t.store.old == nil || b.Version != t.store.old.Version {
		return 0, nil
	}
	t.store.updated = b
	return 1, nil
}

func (t *bomUpdateTx) CreateAuditLog(log *model.AuditLog) error {
	t.store.audit = log
	return nil
}

func sampleBOMItem() *model.BOMItem {
	remark := "原备注"
	return &model.BOMItem{
		ID: 11, ProductID: 3, PartID: 7, Quantity: 2, LossRate: 1.5,
		Remark: &remark, Replaceable: 1, UseMode: 0, Version: 4,
	}
}

func TestUpdateBOMItemKeepsNonEditableFields(t *testing.T) {
	old := sampleBOMItem()
	store := &bomUpdateStore{old: old}
	svc := NewWithSnapshot(store, nil, nil)

	out, err := svc.UpdateBOMItem(&model.BOMItem{
		ID: 11, Quantity: 8, LossRate: 3, UseMode: 1, Replaceable: 0, Version: 4,
		Operator: ptr("admin"),
	})
	if err != nil {
		t.Fatalf("UpdateBOMItem: %v", err)
	}
	if store.updated == nil {
		t.Fatal("仓储层没有被调用")
	}
	if store.updated.PartID != old.PartID || store.updated.ProductID != old.ProductID {
		t.Fatalf("零件归属被改动：%+v", store.updated)
	}
	if store.updated.Remark == nil || *store.updated.Remark != *old.Remark {
		t.Fatalf("备注被改动：%+v", store.updated)
	}
	if store.updated.Quantity != 8 || store.updated.LossRate != 3 || store.updated.UseMode != 1 ||
		store.updated.Replaceable != 0 {
		t.Fatalf("四个字段没写进去：%+v", store.updated)
	}
	if out.Version != 5 {
		t.Fatalf("返回的 version=%d，期望 5", out.Version)
	}
	if store.audit == nil || store.audit.Action != "UPDATE" || store.audit.TableName != "bom_items" {
		t.Fatalf("审计缺失或不对：%+v", store.audit)
	}
	// 审计里新旧两边都必须是完整行：只写"用量 2→8"的话，
	// 事后没法还原当时挂的是哪个零件、什么备注。
	if store.audit.NewData == nil || (*store.audit.NewData)["part_id"] != float64(7) {
		t.Fatalf("审计 new 值缺少零件归属：%v", store.audit.NewData)
	}
	if (*store.audit.NewData)["quantity"] != float64(8) {
		t.Fatalf("审计 new 值用量不对：%v", *store.audit.NewData)
	}
}

func TestUpdateBOMItemRejectsInvalidValues(t *testing.T) {
	cases := []struct {
		name string
		item *model.BOMItem
		want string
	}{
		{"zero quantity", &model.BOMItem{ID: 11, Quantity: 0, Version: 4}, "BOM用量必须是大于0的有限数值"},
		{"negative quantity", &model.BOMItem{ID: 11, Quantity: -1, Version: 4}, "BOM用量必须是大于0的有限数值"},
		{"loss above 100", &model.BOMItem{ID: 11, Quantity: 1, LossRate: 101, Version: 4}, "损耗率必须在0到100之间"},
		{"negative loss", &model.BOMItem{ID: 11, Quantity: 1, LossRate: -0.1, Version: 4}, "损耗率必须在0到100之间"},
		{"bad use mode", &model.BOMItem{ID: 11, Quantity: 1, UseMode: 2, Version: 4}, "BOM用量模式不合法"},
		{"bad replaceable", &model.BOMItem{ID: 11, Quantity: 1, Replaceable: 2, Version: 4}, "BOM可替换标记不合法"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			store := &bomUpdateStore{old: sampleBOMItem()}
			svc := NewWithSnapshot(store, nil, nil)
			c.item.Operator = ptr("admin")
			_, err := svc.UpdateBOMItem(c.item)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err=%v, 期望包含 %q", err, c.want)
			}
			if store.updated != nil {
				t.Fatal("校验失败却写了库")
			}
		})
	}
}

func TestUpdateBOMItemReportsMissingAndConflict(t *testing.T) {
	svc := NewWithSnapshot(&bomUpdateStore{old: nil}, nil, nil)
	_, err := svc.UpdateBOMItem(&model.BOMItem{ID: 11, Quantity: 1, Version: 4, Operator: ptr("admin")})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("不存在的明细：err=%v", err)
	}

	// 存在但版本不符：仓储返回 0 行
	store := &bomUpdateStore{old: sampleBOMItem()}
	store.old.Version = 9 // 调用方传来的 4 与库里的 9 不符
	svc = NewWithSnapshot(store, nil, nil)
	_, err = svc.UpdateBOMItem(&model.BOMItem{ID: 11, Quantity: 1, Version: 4, Operator: ptr("admin")})
	if err == nil {
		t.Fatal("版本冲突必须报错")
	}
}

func ptr(s string) *string { return &s }
