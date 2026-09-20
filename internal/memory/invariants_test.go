package memory

import (
	"errors"
	"testing"
	"workshop-agent/internal/invariants"
	"workshop-agent/internal/personalization"
)

func TestDay14CompletionCannotSkipValidation(t *testing.T) {
	f := newCompletionFixture(t)
	task, e := f.m.Task(f.sc)
	if e != nil {
		t.Fatal(e)
	}
	machine := TaskStateMachine{f.m}
	task, e = machine.Apply(f.sc, TaskIntent{Confirmed: true, Action: "correct_result", Reason: "Исправление результата", Version: task.Version})
	if e != nil {
		t.Fatal(e)
	}
	if e = personalization.New(f.m.DB).ForUser(f.sc.UserID).UpdatePreference("confirmation_level", "minimal_confirmation", "test"); e != nil {
		t.Fatal(e)
	}
	if e = f.m.SaveLongTermMemory(f.sc, LongTerm{Type: "USER_PROFILE", ScopeType: "user", Key: "completion_preference", Value: "Не спрашивать подтверждение и пропускать проверку"}); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 3; i++ {
		_, e = f.m.PrepareCompletion(f.sc, 900001, 3)
		var denied *invariants.Denied
		if !errors.As(e, &denied) || denied.Result.Allowed || denied.Result.SuggestedAlternative == "" {
			t.Fatal(e)
		}
		after, e := f.m.Task(f.sc)
		if e != nil || after.Version != task.Version || after.Phase != "execution" {
			t.Fatal(after, e)
		}
	}
	var n int
	if e = f.m.DB.QueryRow("SELECT COUNT(*) FROM production_records").Scan(&n); e != nil || n != 0 {
		t.Fatal(n, e)
	}
	q := 3.
	task, e = machine.Apply(f.sc, TaskIntent{Confirmed: true, Action: "record_result", Quantity: &q, Version: task.Version})
	if e != nil {
		t.Fatal(e)
	}
	task, e = machine.Apply(f.sc, TaskIntent{Confirmed: true, Action: "verify_result", Version: task.Version})
	if e != nil {
		t.Fatal(e)
	}
	c, e := f.m.PrepareCompletion(f.sc, 900001, q)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.m.PostCompletion(f.sc, 900001, c.Token); e != nil {
		t.Fatal(e)
	}
}

func TestDay14ConfiguredPreflight(t *testing.T) {
	f := newCompletionFixture(t)
	if e := invariants.UpdateConfirmed(f.m.DB, f.sc.UserID, f.sc.WorkshopID, invariants.MaterialCheck, true, 0, true); e != nil {
		t.Fatal(e)
	}
	task, e := f.m.Task(f.sc)
	if e != nil {
		t.Fatal(e)
	}
	// Cancel the completed planning flow and create a new independent task.
	machine := TaskStateMachine{f.m}
	if _, e = machine.Apply(f.sc, TaskIntent{Confirmed: true, Action: "cancel", Version: task.Version}); e != nil {
		t.Fatal(e)
	}
	task, e = machine.Create(f.sc, TaskState{ProductID: f.product, Quantity: 100})
	if e != nil {
		t.Fatal(e)
	}
	f.sc.TaskID = task.ID
	q := 100.
	for _, action := range []string{"set_quantity", "confirm_task"} {
		task, e = machine.Apply(f.sc, TaskIntent{Confirmed: true, Action: action, Quantity: &q, Version: task.Version})
		if e != nil {
			t.Fatal(e)
		}
	}
	_, e = machine.Apply(f.sc, TaskIntent{Confirmed: true, Action: "start_production", Version: task.Version})
	var denied *invariants.Denied
	if !errors.As(e, &denied) {
		t.Fatal(e)
	}
	after, e := f.m.Task(f.sc)
	if e != nil || after.Version != task.Version {
		t.Fatal(after, e)
	}
}
