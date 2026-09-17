package products

import (
	"errors"
	"path/filepath"
	"testing"
	"workshop-agent/internal/testkit"
)

func TestRequirementsUnitsLossAndIncompleteNestedBOM(t *testing.T) {
	path := filepath.Join(t.TempDir(), "requirements.db")
	user, w := testkit.Owner(t, path)
	s := NewBOMService(path).ForUser(user)
	defer s.Close()
	if _, err := s.DB().Exec("INSERT INTO materials(workshop_id,name,category,base_unit) VALUES(?,'Гипс','raw','g')", w); err != nil {
		t.Fatal(err)
	}
	child, err := s.CreateProduct(w, "Child", "c", "product", 0, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	parent, err := s.CreateProduct(w, "Parent", "p", "kit", 0, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetBOMItem(w, parent, "product", 0, child, 2, "pcs", 0, ""); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Requirements(w, parent, 5); !errors.Is(err, ErrIncompleteBOM) {
		t.Fatal("empty nested BOM", err)
	}
	if err = s.SetBOMItem(w, child, "material", 1, 0, 0.5, "kg", 10, ""); err != nil {
		t.Fatal(err)
	}
	needs, err := s.Requirements(w, parent, 5)
	if err != nil || needs[1] != 5500 {
		t.Fatal(needs, err)
	}
	if err = s.SetBOMItem(w, child, "product", 0, parent, 1, "pcs", 0, ""); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Requirements(w, parent, 5); !errors.Is(err, ErrIncompleteBOM) {
		t.Fatal("cycle", err)
	}
}
