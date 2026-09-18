package invariants_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"workshop-agent/internal/auth"
	"workshop-agent/internal/invariants"
	"workshop-agent/internal/inventory"
	"workshop-agent/internal/storage"
	"workshop-agent/internal/testkit"
)

func TestDay14PersistenceAndAuthorization(t *testing.T) {
	if path := os.Getenv("DAY14_TEST_DB"); path != "" {
		s, e := storage.New(path)
		if e != nil {
			t.Fatal(e)
		}
		defer s.Close()
		rules, e := (invariants.InvariantRegistry{}).Workshop(s.DB, 1, 1)
		if e != nil || !rules[len(rules)-1].IsActive || rules[len(rules)-1].Version != 1 {
			t.Fatal(rules, e)
		}
		return
	}
	path := filepath.Join(t.TempDir(), "day14.db")
	u, w := testkit.Owner(t, path)
	s, e := storage.New(path)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if e = invariants.UpdateConfirmed(s.DB, u, w, invariants.MaterialCheck, true, 0, false); !errors.Is(e, invariants.ErrConfirmation) {
		t.Fatal(e)
	}
	if e = invariants.UpdateConfirmed(s.DB, u, w, "nonnegative_inventory", false, 0, true); !errors.Is(e, invariants.ErrProtected) {
		t.Fatal(e)
	}
	if e = invariants.UpdateConfirmed(s.DB, u, w, invariants.MaterialCheck, true, 0, true); e != nil {
		t.Fatal(e)
	}
	if e = invariants.UpdateConfirmed(s.DB, u, w, invariants.MaterialCheck, false, 0, true); !errors.Is(e, invariants.ErrVersion) {
		t.Fatal(e)
	}
	if e = invariants.UpdateConfirmed(s.DB, u+999, w, invariants.MaterialCheck, false, 1, true); !errors.Is(e, auth.ErrDenied) {
		t.Fatal(e)
	}
	if _, e = (invariants.InvariantRegistry{}).Workshop(s.DB, u, w+999); !errors.Is(e, auth.ErrDenied) {
		t.Fatal(e)
	}
	var n int
	if e = s.DB.QueryRow("SELECT COUNT(*) FROM audit_logs WHERE event_type='INVARIANT_UPDATED'").Scan(&n); e != nil || n != 1 {
		t.Fatal(n, e)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestDay14PersistenceAndAuthorization$")
	cmd.Env = append(os.Environ(), "DAY14_TEST_DB="+path)
	if out, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("new process: %v %s", e, out)
	}
	inv := inventory.NewService(path)
	defer inv.Close()
	id, e := inv.ForUser(u).CreateMaterial(w, "Brush", "raw", "pcs", 400, 0, "", 0, "")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = inv.ForUser(u).AdjustStockByID(w, id, -500); e == nil {
		t.Fatal("negative stock allowed")
	}
	var stock float64
	if e = s.DB.QueryRow("SELECT current_stock FROM materials WHERE id=?", id).Scan(&stock); e != nil || stock != 400 {
		t.Fatal(stock, e)
	}
	if _, e = inv.ForUser(u).AdjustStockByID(w, id, -5); e != nil {
		t.Fatal(e)
	}
	for _, action := range []string{"direct_sql_write", "replace_stack", "remove_last_owner", "direct_task_state"} {
		r := (invariants.InvariantEngine{}).Evaluate(s.DB, invariants.ProposedAction{ActionType: action, UserID: u, WorkshopID: w}, invariants.Facts{})
		if r.Allowed || r.SuggestedAlternative == "" {
			t.Fatal(r)
		}
	}
	r := (invariants.InvariantEngine{}).Evaluate(s.DB, invariants.ProposedAction{ActionType: "change_stock", UserID: u, WorkshopID: w}, invariants.Facts{ResultingStock: 0})
	if !r.Allowed || r.Decision != "WARN" || len(r.Warnings) != 1 {
		t.Fatal(r)
	}
	if _, e = s.DB.Exec("INSERT INTO users(telegram_user_id,first_name) VALUES(900002,'Employee')"); e != nil {
		t.Fatal(e)
	}
	var employee int64
	if e = s.DB.QueryRow("SELECT id FROM users WHERE telegram_user_id=900002").Scan(&employee); e != nil {
		t.Fatal(e)
	}
	if _, e = s.DB.Exec("INSERT INTO workshop_members(workshop_id,user_id,role,joined_at) VALUES(?,?,'EMPLOYEE',CURRENT_TIMESTAMP)", w, employee); e != nil {
		t.Fatal(e)
	}
	r = (invariants.InvariantEngine{}).Evaluate(s.DB, invariants.ProposedAction{ActionType: "change_bom", UserID: employee, WorkshopID: w}, invariants.Facts{Confirmed: true})
	if r.Allowed {
		t.Fatal(r)
	}
	if e = invariants.UpdateConfirmed(s.DB, employee, w, invariants.MaterialCheck, false, 1, true); !errors.Is(e, auth.ErrDenied) {
		t.Fatal(e)
	}
}

func TestDay14Scopes(t *testing.T) {
	id := int64(2)
	c := invariants.ScopeContext{WorkshopID: 2, ModuleID: 2, ProcessID: 2, ProductID: 2, RoleID: 2}
	for _, scope := range []string{"WORKSHOP", "MODULE", "PROCESS", "PRODUCT", "ROLE"} {
		r := invariants.Rule{ScopeType: scope, ScopeID: &id}
		if !invariants.Applies(r, c) || invariants.Applies(r, invariants.ScopeContext{}) {
			t.Fatal(scope)
		}
	}
	if !invariants.Applies(invariants.Rule{ScopeType: "SYSTEM"}, c) {
		t.Fatal("system")
	}
}
