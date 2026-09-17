package inventory

import (
	"path/filepath"
	"testing"
	"workshop-agent/internal/testkit"
)

func TestUpdateMaterialAndAudit(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "audit_test.db")
	svc := NewService(dbPath)
	defer svc.Close()
	userID, _ := testkit.Owner(t, dbPath)
	svc = svc.ForUser(userID)

	if _, err := svc.CreateMaterial(1, "гипс", "raw_material", "g", 1000, 100, "", 7, ""); err != nil {
		t.Fatalf("create material: %v", err)
	}

	if err := svc.UpdateMaterial(1, 1, "current_stock", "2500", 42, "admin"); err != nil {
		t.Fatalf("update material: %v", err)
	}

	stock, err := svc.GetMaterialStock(1, "гипс")
	if err != nil {
		t.Fatalf("get material stock: %v", err)
	}
	if stock != 2500 {
		t.Fatalf("expected stock 2500, got %v", stock)
	}
}
