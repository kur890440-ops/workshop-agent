package products

import (
	"errors"
	"path/filepath"
	"testing"
	"workshop-agent/internal/auth"
	"workshop-agent/internal/testkit"
)

func editFixture(t *testing.T) (*Service, int64, int64) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "edit.db")
	u, w := testkit.Owner(t, path)
	s := NewBOMService(path).ForUser(u)
	t.Cleanup(func() { s.Close() })
	id, err := s.CreateProduct(w, "Product", "00001", "kit", 5, 2, "")
	if err != nil {
		t.Fatal(err)
	}
	return s, w, id
}
func TestEditProductFieldsAndMovements(t *testing.T) {
	s, w, id := editFixture(t)
	cases := []struct {
		field, value string
		relative     bool
		want         string
	}{
		{"name", " Товары ", false, "Товары"}, {"sku", "00002", false, "00002"}, {"sku", "-", false, ""}, {"product_type", "semi_finished", false, "semi_finished"},
		{"minimum_stock", "0", false, "0"}, {"minimum_stock", "2,5", false, "2.5"}, {"current_stock", "10", false, "10"}, {"current_stock", "+3", true, "13"}, {"current_stock", "-2,5", true, "10.5"}, {"current_stock", "0", false, "0"},
	}
	for _, tc := range cases {
		p, err := s.Product(w, id)
		if err != nil {
			t.Fatal(err)
		}
		old := p.Field(tc.field)
		r, err := s.ApplyEdit(w, id, Edit{Field: tc.field, Value: tc.value, Relative: tc.relative, Expected: &old})
		if err != nil || !r.Changed || r.After != tc.want {
			t.Fatal(tc, r, err)
		}
	}
	var moves, audits int
	var delta float64
	if err := s.DB().QueryRow("SELECT COUNT(*),SUM(quantity) FROM product_movements WHERE product_id=? AND user_id=?", id, s.userID).Scan(&moves, &delta); err != nil {
		t.Fatal(err)
	}
	if err := s.DB().QueryRow("SELECT COUNT(*) FROM audit_logs WHERE event_type='PRODUCT_UPDATED' AND actor_user_id=?", s.userID).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if moves != 4 || delta != -5 || audits != len(cases) {
		t.Fatal(moves, delta, audits)
	}
	r, err := s.ApplyEdit(w, id, Edit{Field: "current_stock", Value: "0"})
	if err != nil || r.Changed {
		t.Fatal("unchanged", r, err)
	}
	var count int
	s.DB().QueryRow("SELECT COUNT(*) FROM product_movements").Scan(&count)
	if count != moves {
		t.Fatal("zero movement")
	}
	s.DB().QueryRow("SELECT COUNT(*) FROM audit_logs WHERE event_type='PRODUCT_UPDATED'").Scan(&count)
	if count != audits {
		t.Fatal("zero audit")
	}
}
func TestEditProductValidationAndConflict(t *testing.T) {
	s, w, id := editFixture(t)
	for _, v := range []string{"NaN", "Inf", "1e999", "1e2", "--2", "2 3", "+0", "-0", "-6", "1000000000001"} {
		relative := len(v) > 0 && (v[0] == '+' || v[0] == '-')
		if _, err := s.ApplyEdit(w, id, Edit{Field: "current_stock", Value: v, Relative: relative}); !errors.Is(err, ErrInvalidEdit) {
			t.Fatal(v, err)
		}
	}
	for _, e := range []Edit{{Field: "name", Value: " "}, {Field: "product_type", Value: "wrong"}, {Field: "id", Value: "42"}, {Field: "minimum_stock", Value: "-1"}} {
		if _, err := s.ApplyEdit(w, id, e); !errors.Is(err, ErrInvalidEdit) {
			t.Fatal(e, err)
		}
	}
	old := "5"
	if _, err := s.ApplyEdit(w, id, Edit{Field: "current_stock", Value: "8"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplyEdit(w, id, Edit{Field: "current_stock", Value: "+3", Relative: true, Expected: &old}); !errors.Is(err, ErrChanged) {
		t.Fatal(err)
	}
	p, err := s.Product(w, id)
	if err != nil || p.Stock != 8 {
		t.Fatal(p, err)
	}
	old = p.Field("current_stock")
	r, err := s.ApplyEdit(w, id, Edit{Field: "current_stock", Value: "+3", Relative: true, Expected: &old})
	if err != nil || r.After != "11" {
		t.Fatal(r, err)
	}
}
func TestEditProductRollbackAndAuthorization(t *testing.T) {
	for _, table := range []string{"product_movements", "audit_logs"} {
		t.Run(table, func(t *testing.T) {
			s, w, id := editFixture(t)
			if _, err := s.DB().Exec("CREATE TRIGGER fail_edit BEFORE INSERT ON " + table + " BEGIN SELECT RAISE(ABORT,'test failure'); END"); err != nil {
				t.Fatal(err)
			}
			if _, err := s.ApplyEdit(w, id, Edit{Field: "current_stock", Value: "+2", Relative: true}); err == nil {
				t.Fatal("expected failure")
			}
			p, _ := s.Product(w, id)
			if p.Stock != 5 {
				t.Fatal("partial stock")
			}
			var n int
			s.DB().QueryRow("SELECT COUNT(*) FROM product_movements").Scan(&n)
			if n != 0 {
				t.Fatal("partial movement")
			}
		})
	}
	s, w, id := editFixture(t)
	if _, err := s.ApplyEdit(w, id+100, Edit{Field: "name", Value: "X"}); !errors.Is(err, auth.ErrDenied) {
		t.Fatal("foreign product", err)
	}
	if _, err := s.DB().Exec("UPDATE workshop_members SET role='VIEWER' WHERE user_id=?", s.userID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplyEdit(w, id, Edit{Field: "name", Value: "X"}); !errors.Is(err, auth.ErrDenied) {
		t.Fatal("viewer", err)
	}
}
