package service

import (
	"errors"
	"strings"
	"testing"

	"app/internal/model"
	"app/internal/repository"
	"app/internal/usecase"
)

type partDeleteStore struct {
	repository.Store
	part    *model.Part
	usedBy  []model.ProductRef
	traces  []model.PartTraceRef
	deleted bool
}

func (s *partDeleteStore) WithTx(fn func(repository.TxOps) error) error {
	return fn(&partDeleteTx{store: s})
}

type partDeleteTx struct {
	repository.TxOps
	store *partDeleteStore
}

func (t *partDeleteTx) GetPartForUpdate(int64) (*model.Part, error) { return t.store.part, nil }

func (t *partDeleteTx) BOMProductsForPart(int64) ([]model.ProductRef, error) {
	return t.store.usedBy, nil
}

func (t *partDeleteTx) TracesForPart(int64) ([]model.PartTraceRef, error) {
	return t.store.traces, nil
}

func (t *partDeleteTx) DeletePart(int64) error {
	t.store.deleted = true
	return nil
}

func (t *partDeleteTx) CreateAuditLog(*model.AuditLog) error { return nil }

// 被 BOM 引用时：拒绝删除，并把引用它的产品带回去。
func TestDeletePartRefusesWhenReferencedByBOM(t *testing.T) {
	store := &partDeleteStore{
		part: &model.Part{ID: 7, Code: "M-001", Name: "共用零件"},
		usedBy: []model.ProductRef{
			{ID: 1, Code: "P-001", Name: "产品一"},
			{ID: 2, Code: "P-002", Name: "产品二"},
		},
	}
	svc := NewWithSnapshot(store, nil, nil)

	err := svc.DeletePart(7, "管理员")
	var inBOM *usecase.PartInBOMError
	if !errors.As(err, &inBOM) {
		t.Fatalf("err = %v，期望 PartInBOMError", err)
	}
	if store.deleted {
		t.Fatal("被引用时仍执行了删除")
	}
	if len(inBOM.Products) != 2 || inBOM.Products[0].Code != "P-001" {
		t.Fatalf("带回的产品 = %+v", inBOM.Products)
	}
	if !strings.Contains(err.Error(), "M-001") || !strings.Contains(err.Error(), "2 个产品") {
		t.Fatalf("错误文案说不清原因：%v", err)
	}
}

// 只被一个产品引用时，文案直接点名那个产品——用户不用再去数。
func TestDeletePartErrorNamesTheSingleProduct(t *testing.T) {
	store := &partDeleteStore{
		part:   &model.Part{ID: 7, Code: "M-001"},
		usedBy: []model.ProductRef{{ID: 1, Code: "P-001", Name: "产品一"}},
	}
	svc := NewWithSnapshot(store, nil, nil)

	err := svc.DeletePart(7, "管理员")
	if err == nil {
		t.Fatal("期望拒绝删除")
	}
	for _, want := range []string{"M-001", "P-001", "产品一"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("文案缺少 %q：%v", want, err)
		}
	}
}

// 没有被引用时照常删除。
func TestDeletePartProceedsWithoutBOMReferences(t *testing.T) {
	store := &partDeleteStore{part: &model.Part{ID: 7, Code: "M-002"}}
	svc := NewWithSnapshot(store, nil, nil)

	if err := svc.DeletePart(7, "管理员"); err != nil {
		t.Fatalf("删除失败：%v", err)
	}
	if !store.deleted {
		t.Fatal("没有被引用却没有删除")
	}
}

// 被生产投料记录引用：拒绝删除，并说明涉及哪些批次、共几条。
func TestDeletePartRefusesWhenReferencedByTraces(t *testing.T) {
	store := &partDeleteStore{
		part: &model.Part{ID: 7, Code: "M-003"},
		traces: []model.PartTraceRef{
			{BatchNo: "B-001", Records: 2},
			{BatchNo: "B-002", Records: 1},
		},
	}
	svc := NewWithSnapshot(store, nil, nil)

	err := svc.DeletePart(7, "管理员")
	var inTrace *usecase.PartInTraceError
	if !errors.As(err, &inTrace) {
		t.Fatalf("err = %v，期望 PartInTraceError", err)
	}
	if store.deleted {
		t.Fatal("被追溯记录引用时仍执行了删除")
	}
	if inTrace.Records() != 3 || len(inTrace.Batches) != 2 {
		t.Fatalf("汇总不对：records=%d batches=%+v", inTrace.Records(), inTrace.Batches)
	}
	for _, want := range []string{"M-003", "3 条", "2 个批次", "B-001"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("文案缺少 %q：%v", want, err)
		}
	}
}

// 两者同时存在时先报 BOM：那条是用户自己能解开的，先给可操作的建议。
func TestDeletePartPrefersBOMOverTraces(t *testing.T) {
	store := &partDeleteStore{
		part:   &model.Part{ID: 7, Code: "M-004"},
		usedBy: []model.ProductRef{{ID: 1, Code: "P-001", Name: "产品一"}},
		traces: []model.PartTraceRef{{BatchNo: "B-001", Records: 1}},
	}
	svc := NewWithSnapshot(store, nil, nil)

	err := svc.DeletePart(7, "管理员")
	var inBOM *usecase.PartInBOMError
	if !errors.As(err, &inBOM) {
		t.Fatalf("err = %v，期望先报 PartInBOMError", err)
	}
	if store.deleted {
		t.Fatal("被引用时仍执行了删除")
	}
}
