package usecase

import (
	"testing"

	"app/internal/auth"
)

func TestPermissionForCoversKeyUseCases(t *testing.T) {
	want := []string{
		"Inventory.StockIn",
		"Inventory.AdjustStock",
		"Production.UpdateBatchStatus",
		"Production.RevokeBatch",
		"Backup.ClearDatabase",
		"Identity.CreateUser",
	}
	for _, name := range want {
		if _, ok := PermissionFor(name); !ok {
			t.Fatalf("missing permission point %q", name)
		}
	}
}

func TestAllowMatchesModuleMatrix(t *testing.T) {
	if !Allow(auth.RoleWarehouse, "Inventory.StockIn") {
		t.Fatal("warehouse should stock in")
	}
	if Allow(auth.RoleWarehouse, "Catalog.CreateProduct") {
		t.Fatal("warehouse must not create products")
	}
	if !Allow(auth.RoleProduction, "Production.UpdateBatchStatus") {
		t.Fatal("production should update batch status")
	}
	if Allow(auth.RoleViewer, "Backup.ClearDatabase") {
		t.Fatal("viewer must not clear database")
	}
	if !Allow(auth.RoleAdmin, "Identity.CreateUser") {
		t.Fatal("admin should create users")
	}
	if Allow(auth.RoleViewer, "Identity.CreateUser") {
		t.Fatal("viewer must not create users")
	}
}

func TestUnknownUseCaseDenied(t *testing.T) {
	if Allow(auth.RoleAdmin, "Does.Not.Exist") {
		t.Fatal("unknown use case must be denied")
	}
}
