package products

import (
	"errors"
	"testing"
	"workshop-agent/internal/auth"
	"workshop-agent/internal/inventory"
)

func compositionFixture(t *testing.T) (*Service, int64, int64) {
	t.Helper()
	s, w, p := editFixture(t)
	for _, q := range []string{"INSERT INTO materials(workshop_id,name,category,base_unit,display_unit) VALUES(?,'Гипс','raw','g','kg')", "INSERT INTO materials(workshop_id,name,category,base_unit,display_unit) VALUES(?,'Коробка','packaging','pcs','pcs')", "INSERT INTO materials(workshop_id,name,category,base_unit,display_unit) VALUES(?,'Пакет','packaging','pcs','pcs')"} {
		if _, err := s.DB().Exec(q, w); err != nil {
			t.Fatal(err)
		}
	}
	return s, w, p
}
func changeBOM(t *testing.T, s *Service, w, p int64, c BOMChange) bool {
	t.Helper()
	snap, err := s.Composition(w, p)
	if err != nil {
		t.Fatal(err)
	}
	c.Expected = snap.Version
	changed, err := s.ApplyBOMChange(w, p, c)
	if err != nil {
		t.Fatal(c, err)
	}
	return changed
}
func TestCompositionCRUDUnitsAndDomainIsolation(t *testing.T) {
	s, w, p := compositionFixture(t)
	for _, c := range []BOMChange{{Action: "add", MaterialID: 1, Value: "0,25", DisplayUnit: "kg"}, {Action: "add", MaterialID: 2, Value: "1", DisplayUnit: "pcs"}, {Action: "add", MaterialID: 3, Value: "1", DisplayUnit: "pcs"}} {
		changeBOM(t, s, w, p, c)
	}
	snap, err := s.Composition(w, p)
	if err != nil {
		t.Fatal(err)
	}
	r := snap.Rows[0]
	if len(snap.Rows) != 3 || r.Quantity != 250 || r.Unit != "g" || r.DisplayQuantity() != "0.25 кг" {
		t.Fatal(snap)
	}
	needs, err := s.Requirements(w, p, 5)
	if err != nil || needs[1] != 1250 || needs[2] != 5 || needs[3] != 5 {
		t.Fatal(needs, err)
	}
	if _, err = s.DB().Exec("UPDATE bom_items SET technical_loss_percent=7,notes='preserve' WHERE id=?", r.ID); err != nil {
		t.Fatal(err)
	}
	changeBOM(t, s, w, p, BOMChange{Action: "update", RowID: r.ID, Value: "0.5", DisplayUnit: "kg"})
	snap, _ = s.Composition(w, p)
	r = snap.Rows[0]
	if r.Quantity != 500 || r.Loss != 7 || r.Notes != "preserve" {
		t.Fatal("metadata changed", r)
	}
	if changeBOM(t, s, w, p, BOMChange{Action: "update", RowID: r.ID, Value: "0,5", DisplayUnit: "kg"}) {
		t.Fatal("no-op marked changed")
	}
	changeBOM(t, s, w, p, BOMChange{Action: "remove", RowID: r.ID})
	for _, table := range []string{"inventory_movements", "product_movements", "production_records"} {
		var n int
		if err = s.DB().QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil || n != 0 {
			t.Fatal(table, n, err)
		}
	}
	var n int
	s.DB().QueryRow("SELECT COUNT(*) FROM materials").Scan(&n)
	if n != 3 {
		t.Fatal("removed material")
	}
	s.DB().QueryRow("SELECT COUNT(*) FROM audit_logs WHERE event_type='BOM_COMPOSITION_CHANGED' AND actor_user_id=?", s.userID).Scan(&n)
	if n != 5 {
		t.Fatal("audit count", n)
	}
}
func TestCompositionConflictDuplicateAndUnits(t *testing.T) {
	s, w, p := compositionFixture(t)
	snap, _ := s.Composition(w, p)
	c := BOMChange{Action: "add", MaterialID: 1, Value: "0.25", DisplayUnit: "kg", Expected: snap.Version}
	changeBOM(t, s, w, p, BOMChange{Action: "add", MaterialID: 2, Value: "1", DisplayUnit: "pcs"})
	if _, err := s.ApplyBOMChange(w, p, c); !errors.Is(err, ErrChanged) {
		t.Fatal(err)
	}
	snap, _ = s.Composition(w, p)
	c.Expected = snap.Version
	if _, err := s.ApplyBOMChange(w, p, c); err != nil {
		t.Fatal(err)
	}
	snap, _ = s.Composition(w, p)
	c.Expected = snap.Version
	if _, err := s.ApplyBOMChange(w, p, c); !errors.Is(err, ErrBOMDuplicate) {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec("UPDATE materials SET display_unit='g' WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	c.Action = "update"
	c.RowID = snap.Rows[1].ID
	if _, err := s.ApplyBOMChange(w, p, c); !errors.Is(err, inventory.ErrUnitChanged) {
		t.Fatal("changed unit", err)
	}
	for _, v := range []string{"0", "-1", "+1", "NaN", "Inf", "1e400", "2 3", "1000000000001"} {
		if _, err := BOMQuantity(v, "kg", "g"); err == nil {
			t.Fatal(v)
		}
	}
}
func TestCompositionNestedRollbackAndPermissions(t *testing.T) {
	s, w, p := compositionFixture(t)
	child, err := s.CreateProduct(w, "Nested", "n", "product", 0, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetBOMItem(w, p, "product", 0, child, 2, "pcs", 3, "keep"); err != nil {
		t.Fatal(err)
	}
	snap, _ := s.Composition(w, p)
	r := snap.Rows[0]
	changeBOM(t, s, w, p, BOMChange{Action: "update", RowID: r.ID, Value: "3", DisplayUnit: "pcs"})
	snap, _ = s.Composition(w, p)
	if snap.Rows[0].Kind != "product" || snap.Rows[0].ProductID != child || snap.Rows[0].Loss != 3 {
		t.Fatal(snap)
	}
	if _, err = s.DB().Exec("CREATE TRIGGER fail_composition BEFORE INSERT ON audit_logs WHEN NEW.event_type='BOM_COMPOSITION_CHANGED' BEGIN SELECT RAISE(ABORT,'test'); END"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ApplyBOMChange(w, p, BOMChange{Action: "remove", RowID: r.ID, Expected: snap.Version}); err == nil {
		t.Fatal("expected rollback")
	}
	after, _ := s.Composition(w, p)
	if after.Version != snap.Version {
		t.Fatal("partial delete")
	}
	if _, err = s.DB().Exec("UPDATE workshop_members SET role='VIEWER' WHERE user_id=?", s.userID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ApplyBOMChange(w, p, BOMChange{Action: "remove", RowID: r.ID, Expected: snap.Version}); !errors.Is(err, auth.ErrDenied) {
		t.Fatal(err)
	}
	if _, err = s.Composition(w, p); err != nil {
		t.Fatal("viewer cannot read", err)
	}
}

func TestCompositionAddUpdateAuditRollback(t *testing.T) {
	for _, op := range []string{"add", "update"} {
		t.Run(op, func(t *testing.T) {
			s, w, p := compositionFixture(t)
			changeBOM(t, s, w, p, BOMChange{Action: "add", MaterialID: 1, Value: "0.25", DisplayUnit: "kg"})
			snap, _ := s.Composition(w, p)
			if _, err := s.DB().Exec("CREATE TRIGGER fail_audit BEFORE INSERT ON audit_logs WHEN NEW.event_type='BOM_COMPOSITION_CHANGED' BEGIN SELECT RAISE(ABORT,'test'); END"); err != nil {
				t.Fatal(err)
			}
			c := BOMChange{Action: op, RowID: snap.Rows[0].ID, MaterialID: 2, Value: "1", DisplayUnit: "kg", Expected: snap.Version}
			if op == "add" {
				c.DisplayUnit = "pcs"
			}
			if _, err := s.ApplyBOMChange(w, p, c); err == nil {
				t.Fatal("expected error")
			}
			after, _ := s.Composition(w, p)
			if after.Version != snap.Version {
				t.Fatal("partial BOM write")
			}
		})
	}
}
