package service

import (
	"math"
	"testing"

	"app/internal/model"
)

func TestValidatePlanQtyRequiresPositiveInteger(t *testing.T) {
	for _, qty := range []int{0, -1} {
		if err := validatePlanQty(qty); err == nil {
			t.Errorf("validatePlanQty(%d) accepted invalid quantity", qty)
		}
	}
	if err := validatePlanQty(1); err != nil {
		t.Fatalf("validatePlanQty(1): %v", err)
	}
}

func TestValidateBOMItemRejectsInvalidQuantityAndLossRate(t *testing.T) {
	cases := []struct {
		name string
		item model.BOMItem
	}{
		{name: "zero quantity", item: model.BOMItem{Quantity: 0, LossRate: 0}},
		{name: "negative quantity", item: model.BOMItem{Quantity: -1, LossRate: 0}},
		{name: "negative loss", item: model.BOMItem{Quantity: 1, LossRate: -0.01}},
		{name: "loss over one hundred percent", item: model.BOMItem{Quantity: 1, LossRate: 100.01}},
		{name: "unknown mode", item: model.BOMItem{Quantity: 1, LossRate: 0, UseMode: 2}},
		{name: "not a number", item: model.BOMItem{Quantity: math.NaN(), LossRate: 0}},
		{name: "infinite loss", item: model.BOMItem{Quantity: 1, LossRate: math.Inf(1)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateBOMItem(&tc.item); err == nil {
				t.Fatal("validateBOMItem accepted invalid BOM item")
			}
		})
	}

	valid := model.BOMItem{Quantity: 1.5, LossRate: 5, UseMode: 1}
	if err := validateBOMItem(&valid); err != nil {
		t.Fatalf("validateBOMItem(valid): %v", err)
	}
}

func TestValidateStockAndTraceQuantitiesRejectNonFiniteValues(t *testing.T) {
	invalid := []float64{-1, math.NaN(), math.Inf(1), math.Inf(-1)}
	for _, value := range invalid {
		if err := validateNonNegativeQuantity("库存", value); err == nil {
			t.Errorf("validateNonNegativeQuantity(%v) accepted invalid value", value)
		}
		if err := validatePositiveQuantity("投料", value); err == nil {
			t.Errorf("validatePositiveQuantity(%v) accepted invalid value", value)
		}
	}
	if err := validateNonNegativeQuantity("库存", 0); err != nil {
		t.Fatalf("zero stock should be valid: %v", err)
	}
	if err := validatePositiveQuantity("入库", 0); err == nil {
		t.Fatal("zero inbound quantity should be rejected")
	}
	if err := validatePositiveQuantity("投料", 0.01); err != nil {
		t.Fatalf("positive trace quantity should be valid: %v", err)
	}
}

func TestServiceRejectsInvalidQuantitiesBeforeDatabaseAccess(t *testing.T) {
	svc := New(nil, "", "", nil)

	if _, err := svc.CreateBatch(&model.ProductBatch{PlanQty: 0}); err == nil {
		t.Fatal("CreateBatch accepted zero plan quantity")
	}
	if _, err := svc.AddBOMItem(&model.BOMItem{Quantity: -1}); err == nil {
		t.Fatal("AddBOMItem accepted negative quantity")
	}
	if err := svc.StockIn(1, 0, "operator"); err == nil {
		t.Fatal("StockIn accepted zero quantity")
	}
	if err := svc.AdjustStock(1, -1, "operator"); err == nil {
		t.Fatal("AdjustStock accepted negative quantity")
	}
	if _, err := svc.CreatePart(&model.Part{StockQty: -1}); err == nil {
		t.Fatal("CreatePart accepted negative stock")
	}
	if _, err := svc.RecordTrace(&model.BatchTrace{UsedQty: 0}); err == nil {
		t.Fatal("RecordTrace accepted zero quantity")
	}
}
