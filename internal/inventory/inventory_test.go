package inventory

import (
	"path/filepath"
	"testing"
	"workshop-agent/internal/testkit"
)

func TestQuantityConversions(t *testing.T) {
	if got := ConvertToBase("kg", 5); got != 5000 {
		t.Fatalf("kg to g mismatch: got %v", got)
	}
	if got := ConvertToBase("g", 230); got != 230 {
		t.Fatalf("g to g mismatch: got %v", got)
	}
	if got := ConvertFromBase("kg", 5000); got != 5 {
		t.Fatalf("kg from base mismatch: got %v", got)
	}
	if got := ConvertToBase("l", 2); got != 2000 {
		t.Fatalf("l to ml mismatch: got %v", got)
	}
	if got := ConvertFromBase("l", 2500); got != 2.5 {
		t.Fatalf("l from base mismatch: got %v", got)
	}
}

func TestMaterialStockBasic(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "inventory_test.db")
	svc := NewService(dbPath)
	defer svc.Close()
	userID, _ := testkit.Owner(t, dbPath)
	svc = svc.ForUser(userID)
	if err := svc.Init(); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateMaterial(1, "Гипс", "raw_material", "g", 0, 1000, "", 7, ""); err != nil {
		t.Fatal(err)
	}
	if err := svc.AdjustStock(1, "Гипс", 5000); err != nil {
		t.Fatal(err)
	}
	stock, err := svc.GetMaterialStock(1, "Гипс")
	if err != nil {
		t.Fatal(err)
	}
	if stock != 5000 {
		t.Fatalf("stock mismatch: got %v", stock)
	}
}

func TestListPurchaseNeeds(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "purchase_needs_test.db")
	svc := NewService(dbPath)
	defer svc.Close()
	userID, _ := testkit.Owner(t, dbPath)
	svc = svc.ForUser(userID)

	if _, err := svc.CreateMaterial(1, "Гипс", "сырьё", "kg", 2, 5, "Поставщик", 3, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateMaterial(1, "Краска", "компонент", "l", 10, 5, "", 0, ""); err != nil {
		t.Fatal(err)
	}

	needs, err := svc.ListPurchaseNeeds(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(needs) != 1 {
		t.Fatalf("expected one purchase need, got %d", len(needs))
	}
	if needs[0]["name"] != "Гипс" || needs[0]["order_quantity"] != 3000.0 {
		t.Fatalf("unexpected purchase need: %#v", needs[0])
	}
}
