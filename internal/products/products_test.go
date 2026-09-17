package products

import (
	"path/filepath"
	"testing"
	"workshop-agent/internal/testkit"
)

func TestBOMNestedAndRequirements(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "bom_test.db")
	bom := NewBOMService(dbPath)
	defer bom.Close()
	userID, _ := testkit.Owner(t, dbPath)
	bom = bom.ForUser(userID)
	if _, err := bom.DB().Exec(`INSERT INTO materials(workshop_id,name,category,base_unit) VALUES(1,'gypsum','raw','g')`); err != nil {
		t.Fatal(err)
	}
	if err := bom.Init(); err != nil {
		t.Fatal(err)
	}

	productID, err := bom.CreateProduct(1, "Stitch", "STITCH", "product", 0, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bom.CreateProduct(1, "Набор Stitch", "KIT1", "kit", 0, 0, ""); err != nil {
		t.Fatal(err)
	}
	if err := bom.SetBOMItem(1, productID, "material", 1, 0, 230, "g", 5, ""); err != nil {
		t.Fatal(err)
	}
	if err := bom.SetBOMItem(1, 2, "product", 0, productID, 1, "pcs", 0, ""); err != nil {
		t.Fatal(err)
	}
	items, err := bom.GetBOM(1, productID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 bom item on the product itself, got %d", len(items))
	}
	if got := CalculateRequirementForProduct(1, 2, 5, bom); len(got) != 1 {
		t.Fatalf("expected nested requirement for 1 material, got %d", len(got))
	}
	if got := CalculateRequirementForProduct(1, 2, 5, bom); int64(got[0]["material_id"].(int64)) != 1 {
		t.Fatalf("unexpected requirement calculation")
	}
}
