package inventory

import (
	"math"
	"path/filepath"
	"strings"
	"testing"
	"workshop-agent/internal/auth"
	"workshop-agent/internal/testkit"
	"workshop-agent/internal/workshops"
)

func TestWorkingUnitsConversionAndAuthorization(t *testing.T) {
	p := filepath.Join(t.TempDir(), "units.db")
	u, w := testkit.Owner(t, p)
	s := NewService(p).ForUser(u)
	defer s.Close()
	for _, tc := range []struct {
		unit string
		base float64
	}{{"g", 10}, {"kg", 10000}, {"ml", 10}, {"l", 10000}, {"pcs", 10}} {
		if ConvertToBase(tc.unit, 10) != tc.base || ConvertFromBase(tc.unit, tc.base) != 10 {
			t.Fatal(tc)
		}
	}
	if FormatQuantity(0.000001, "kg") == "0 кг" || strings.Contains(FormatQuantity(0.1+0.2, "g"), "0000000") {
		t.Fatal("formatting")
	}
	if FormatQuantity(math.SmallestNonzeroFloat64, "kg") == "0 кг" {
		t.Fatal("underflow in formatting")
	}
	for _, tc := range []struct{ text, unit string }{{"500 г гипса", "g"}, {"2 кг гипса", "kg"}, {"2 гипса", "kg"}, {"500 граммов гипса", "g"}} {
		unit, err := InputUnit(tc.text, "g", "kg")
		if err != nil || unit != tc.unit {
			t.Fatal("explicit unit", tc, unit, err)
		}
	}
	id, err := s.CreateMaterial(w, "Гипс", "сырьё", "kg", 10, 2, "", 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetDisplayUnit(w, id, "ml"); err == nil {
		t.Fatal("incompatible unit accepted")
	}
	for _, v := range []float64{math.NaN(), math.Inf(1), 1e308} {
		if _, _, err = s.ChangeDisplayedStock(w, id, v, true, "kg"); err == nil {
			t.Fatal("invalid quantity")
		}
	}
	if _, _, err = s.ChangeDisplayedStock(w, id, -11, false, "kg"); err == nil {
		t.Fatal("negative balance")
	}
	ws := workshops.NewService(p)
	defer ws.Close()
	other, err := ws.CreateOwnedWorkshop(u, "Other")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetDisplayUnit(other, id, "g"); err == nil {
		t.Fatal("foreign material")
	}
	viewer, err := ws.UpsertUser(7777, "", "Viewer", "")
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := ws.CreateInvite(u, w, auth.Viewer, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ws.AcceptInvite(viewer, token); err != nil {
		t.Fatal(err)
	}
	if err = s.ForUser(viewer).SetDisplayUnit(w, id, "g"); err == nil {
		t.Fatal("viewer write")
	}
	item, err := s.ForUser(viewer).Material(w, id)
	if err != nil || Quantity(item, "current_stock") != "10 кг" {
		t.Fatal("viewer read", err)
	}
	if _, err = s.DB().Exec(`CREATE TRIGGER fail_unit_audit BEFORE INSERT ON audit_logs BEGIN SELECT RAISE(ABORT,'test'); END`); err != nil {
		t.Fatal(err)
	}
	if err = s.SetDisplayUnit(w, id, "g"); err == nil {
		t.Fatal("audit failure not propagated")
	}
	item, err = s.Material(w, id)
	if err != nil || DisplayUnit(item) != "kg" {
		t.Fatal("unit rollback failed")
	}
	if _, err = s.DB().Exec(`CREATE TRIGGER fail_unit_movement BEFORE INSERT ON inventory_movements BEGIN SELECT RAISE(ABORT,'test'); END`); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.ChangeDisplayedStock(w, id, 12, true, "kg"); err == nil {
		t.Fatal("movement failure not propagated")
	}
	item, err = s.Material(w, id)
	if err != nil || Quantity(item, "current_stock") != "10 кг" {
		t.Fatal("stock rollback failed")
	}
}
