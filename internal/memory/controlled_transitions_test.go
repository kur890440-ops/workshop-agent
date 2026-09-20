package memory

import (
	"errors"
	"testing"
	"workshop-agent/internal/auth"
	"workshop-agent/internal/storage"
)

func TestDay15ControlledLifecycle(t *testing.T) {
	fixture := newCompletionFixture(t)
	m, sc := fixture.m, fixture.sc
	f := TaskStateMachine{m}
	old, e := m.Task(sc)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.Apply(sc, TaskIntent{Action: "cancel", Version: old.Version}); e != nil {
		t.Fatal(e)
	}
	task, e := f.Create(sc, TaskState{ProductID: fixture.product, Quantity: 5})
	if e != nil {
		t.Fatal(e)
	}
	sc.TaskID = task.ID
	originalID := task.ID
	apply := func(i TaskIntent) {
		t.Helper()
		i.Version = task.Version
		i.Source = "day15_test"
		task, e = f.Request(sc, TransitionRequest{TaskID: task.ID, ActorUserID: sc.UserID, Transition: i.Action, Source: i.Source, Payload: i})
		if e != nil {
			t.Fatal(i.Action, e)
		}
	}
	deny := func(i TaskIntent) {
		t.Helper()
		i.Version = task.Version
		i.Source = "day15_test"
		before := compact(task)
		_, err := f.Apply(sc, i)
		var d *TransitionDenied
		if !errors.As(err, &d) || d.Allowed || d.StateChanged || d.ReasonCode == "" {
			t.Fatal(i.Action, err)
		}
		after, err := m.Task(sc)
		if err != nil || compact(after) != before {
			t.Fatal("denial changed task", err)
		}
	}
	pauseResume := func() {
		t.Helper()
		before := task.CompactState()
		phase, step, expected, state := task.Phase, task.CurrentStep, task.ExpectedAction, compact(task.State)
		apply(TaskIntent{Action: "pause"})
		if task.Phase != phase || task.CurrentStep != step || task.ExpectedAction != expected || compact(task.State) != state {
			t.Fatal(before, task)
		}
		apply(TaskIntent{Action: "resume"})
		if task.Phase != phase || task.CurrentStep != step || task.ExpectedAction != expected || compact(task.State) != state {
			t.Fatal("resume lost state")
		}
	}
	// A, I, N: repeated demands never skip preparation, including for OWNER.
	deny(TaskIntent{Action: "complete"})
	deny(TaskIntent{Action: "complete"})
	pauseResume()
	q := 5.
	apply(TaskIntent{Action: "set_quantity", Quantity: &q})
	// B: filled parameters are not approval.
	deny(TaskIntent{Action: "confirm_task"})
	// L: a previously offered confirmation is stale after a legitimate update.
	stale := task.Version
	apply(TaskIntent{Action: "set_quantity", Quantity: &q})
	if _, e = f.Apply(sc, TaskIntent{Action: "confirm_task", Version: stale, Confirmed: true}); !errors.Is(e, ErrTaskChanged) {
		t.Fatal(e)
	}
	// C, M: one confirmation, one transition.
	approvedVersion := task.Version
	apply(TaskIntent{Action: "confirm_task", Confirmed: true})
	if task.Phase != "execution" || !task.State.ParametersApproved {
		t.Fatal(task)
	}
	if _, e = f.Apply(sc, TaskIntent{Action: "confirm_task", Version: approvedVersion, Confirmed: true}); !errors.Is(e, ErrTaskChanged) {
		t.Fatal(e)
	}
	var count int
	if e = m.DB.QueryRow(`SELECT COUNT(*) FROM task_transitions WHERE task_id=? AND action='confirm_task'`, task.ID).Scan(&count); e != nil || count != 1 {
		t.Fatal(count, e)
	}
	// D, J, P: execution cannot complete; pause survives reopening and a new session.
	deny(TaskIntent{Action: "complete"})
	apply(TaskIntent{Action: "start_production", Confirmed: true})
	apply(TaskIntent{Action: "pause"})
	saved := compact(task.State)
	step := task.CurrentStep
	reopened, e := storage.New(fixture.path)
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close()
	m = New(reopened.DB).ForUser(sc.UserID)
	f = TaskStateMachine{m}
	sc, e = m.EnsureSession(sc, 999000)
	if e != nil {
		t.Fatal(e)
	}
	task, e = m.Task(sc)
	if e != nil {
		t.Fatal(e)
	}
	apply(TaskIntent{Action: "resume"})
	if task.Phase != "execution" || task.CurrentStep != step || compact(task.State) != saved {
		t.Fatal(task)
	}
	// E: a result is required to enter validation.
	deny(TaskIntent{Action: "record_result"})
	apply(TaskIntent{Action: "record_result", Quantity: &q})
	if task.Phase != "validation" || task.State.ValidationResult != "" {
		t.Fatal(task)
	}
	// F, K: merely being in validation does not allow a posting.
	if _, e = m.PrepareCompletion(sc, 999000, q); e == nil {
		t.Fatal("missing validation accepted")
	}
	deny(TaskIntent{Action: "production_posted", Confirmed: true})
	pauseResume()
	// Failed validation returns to execution with its reason.
	deny(TaskIntent{Action: "correct_result"})
	apply(TaskIntent{Action: "correct_result", Reason: "Требуется доработка"})
	if task.Phase != "execution" || task.State.ValidationResult != "failed" || task.State.ValidationReason == "" {
		t.Fatal(task)
	}
	apply(TaskIntent{Action: "record_result", Quantity: &q})
	deny(TaskIntent{Action: "verify_result"})
	apply(TaskIntent{Action: "verify_result", Confirmed: true})
	// O: permission checks remain mandatory after a successful validation.
	if _, e = m.DB.Exec(`UPDATE workshop_members SET role='VIEWER' WHERE user_id=? AND workshop_id=?`, sc.UserID, sc.WorkshopID); e != nil {
		t.Fatal(e)
	}
	if _, e = f.Apply(sc, TaskIntent{Action: "pause", Version: task.Version}); !errors.Is(e, auth.ErrDenied) {
		t.Fatal(e)
	}
	if _, e = m.DB.Exec(`UPDATE workshop_members SET role='OWNER' WHERE user_id=? AND workshop_id=?`, sc.UserID, sc.WorkshopID); e != nil {
		t.Fatal(e)
	}
	// G: only the confirmed atomic posting reaches DONE.
	intent, e := m.PrepareCompletion(sc, 999000, q)
	if e != nil {
		t.Fatal(e)
	}
	receipt, e := m.PostCompletion(sc, 999000, intent.Token)
	if e != nil {
		t.Fatal(e)
	}
	again, e := m.PostCompletion(sc, 999000, intent.Token)
	if e != nil || again.RecordID != receipt.RecordID {
		t.Fatal(again, e)
	}
	task, e = m.Task(sc)
	if e != nil || task.ID != originalID || task.Phase != "done" || task.State.ValidationResult != "passed" {
		t.Fatal(task, e)
	}
	// H: DONE is terminal; ordinary resume cannot reopen it.
	deny(TaskIntent{Action: "resume"})
	trace, e := m.TransitionTrace(sc)
	if e != nil || trace == nil {
		t.Fatal(trace, e)
	}
	var metadata string
	if e = m.DB.QueryRow(`SELECT metadata_json FROM task_transitions WHERE task_id=? AND action='production_posted'`, task.ID).Scan(&metadata); e != nil {
		t.Fatal(e)
	}
	if metadata == "" {
		t.Fatal("missing transition diagnostics")
	}
}

func TestDay15NoForgedTransitionIdentity(t *testing.T) {
	for _, d := range TransitionDefinitions() {
		if d.ToPhase == "done" && (d.Key != "production_posted" || d.FromPhase != "validation" || !d.RequiresConfirmation) {
			t.Fatal("unsafe terminal transition", d)
		}
	}
	f := newCompletionFixture(t)
	task, e := f.m.Task(f.sc)
	if e != nil {
		t.Fatal(e)
	}
	_, e = (TaskStateMachine{f.m}).Request(f.sc, TransitionRequest{TaskID: task.ID, ActorUserID: f.sc.UserID + 1, Transition: "pause", Payload: TaskIntent{Version: task.Version}})
	if !errors.Is(e, ErrScope) {
		t.Fatal(e)
	}
}
