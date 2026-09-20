package memory

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"workshop-agent/internal/auth"
	"workshop-agent/internal/storage"
	"workshop-agent/internal/testkit"
	"workshop-agent/internal/workshops"
)

func TestTaskFSMFlowPauseRestartScope(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.db")
	u, w := testkit.Owner(t, path)
	store, err := storage.New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	s := New(store.DB).ForUser(u)
	sc := Scope{UserID: u, WorkshopID: w}
	_, err = store.DB.Exec("INSERT INTO products(id,workshop_id,name,product_type) VALUES(42,?,'Ballu','product')", w)
	if err != nil {
		t.Fatal(err)
	}
	f := TaskStateMachine{s}
	task, err := f.Create(sc, TaskState{ProductID: 42, ProductName: "Ballu", Quantity: 20, Parameters: map[string]string{"material": "PETG"}})
	if err != nil {
		t.Fatal(err)
	}
	sc.TaskID = task.ID
	apply := func(action string, q *float64) {
		t.Helper()
		task, err = f.Apply(sc, TaskIntent{Confirmed: true, Action: action, Quantity: q, Version: task.Version})
		if err != nil {
			t.Fatal(action, err)
		}
	}
	rejected := func(action string) {
		t.Helper()
		if _, e := f.Apply(sc, TaskIntent{Confirmed: true, Action: action, Version: task.Version}); !errors.Is(e, ErrTransition) {
			t.Fatal(action, e)
		}
	}
	pauseResume := func() {
		t.Helper()
		phase, step, data := task.Phase, task.CurrentStep, compact(task.State)
		apply("pause", nil)
		if task.Phase != phase || task.CurrentStep != step || compact(task.State) != data || task.PausedAt == nil {
			t.Fatal(task)
		}
		apply("resume", nil)
		if task.CurrentStep != step || task.Status != "active" {
			t.Fatal(task)
		}
	}
	rejected("complete")
	pauseResume()
	q := 25.
	apply("set_quantity", &q)
	old := task.Version
	apply("confirm_task", nil)
	if _, e := f.Apply(sc, TaskIntent{Confirmed: true, Action: "start_production", Version: old}); !errors.Is(e, ErrTaskChanged) {
		t.Fatal(e)
	}
	pauseResume()
	apply("pause", nil)
	// A separate OS process reloads and resumes without short-term history.
	cmd := exec.Command(os.Args[0], "-test.run=^TestTaskFSMRestartHelper$")
	cmd.Env = append(os.Environ(), "WA_TASK_TEST_DB="+path, "WA_TASK_TEST_ID="+task.ID)
	if out, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("child process: %v %s", e, out)
	}
	task, err = s.Task(sc)
	if err != nil || task.Status != "active" || task.Phase != "execution" || task.State.Quantity != 25 || task.State.Parameters["material"] != "PETG" {
		t.Fatal(task, err)
	}
	apply("start_production", nil)
	rejected("complete")
	apply("record_result", &q)
	pauseResume()
	apply("verify_result", nil)
	if _, err = f.Apply(sc, TaskIntent{Confirmed: true, Action: "complete", Version: task.Version}); !errors.Is(err, ErrPostingRequired) {
		t.Fatal(err)
	}
	if _, err = store.DB.Exec("INSERT INTO materials(id,workshop_id,name,category,base_unit,current_stock) VALUES(4242,?,'Test material','raw','g',1000)", w); err != nil {
		t.Fatal(err)
	}
	if _, err = store.DB.Exec("INSERT INTO bom_items(workshop_id,product_id,component_type,material_id,quantity,unit,technical_loss_percent,notes) VALUES(?,42,'material',4242,1,'g',0,'')", w); err != nil {
		t.Fatal(err)
	}
	sc, err = s.EnsureSession(sc, 900001)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := s.PrepareCompletion(sc, 900001, 25)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.PostCompletion(sc, 900001, preview.Token); err != nil {
		t.Fatal(err)
	}
	task, err = s.Task(sc)
	if err != nil {
		t.Fatal(err)
	}
	if task.Phase != "done" || task.Status != "completed" || task.CompletedAt == nil {
		t.Fatal(task)
	}
	if task.ID != sc.TaskID {
		t.Fatal("lifecycle changed task ID")
	}
	for _, event := range []string{"TASK_CREATED", "TASK_UPDATED", "TASK_CONFIRMED", "TASK_STARTED", "TASK_PAUSED", "TASK_RESUMED", "TASK_VALIDATION_STARTED", "TASK_COMPLETED"} {
		var n int
		if e := store.DB.QueryRow("SELECT COUNT(*) FROM audit_logs WHERE event_type=?", event).Scan(&n); e != nil || n == 0 {
			t.Fatal(event, n, e)
		}
	}
	rejected("resume")
	if e := s.CompleteWorkingMemory(sc, false); e == nil {
		t.Fatal("legacy bypass")
	}
	history, e := s.TaskHistory(sc)
	if e != nil || len(history) < 10 {
		t.Fatal(history, e)
	}
	outsider := New(store.DB).ForUser(u + 10)
	if _, e = outsider.Task(sc); e == nil {
		t.Fatal("foreign task read")
	}
	ws := workshops.NewService(path)
	defer ws.Close()
	other, err := ws.CreateOwnedWorkshop(u, "B")
	if err != nil {
		t.Fatal(err)
	}
	if _, e = s.Task(sc); e == nil {
		t.Fatal("inactive workshop read")
	}
	sc.WorkshopID = other
	if _, e = s.Task(sc); e == nil {
		t.Fatal("cross-workshop task read")
	}
}
func TestTaskFSMRestartHelper(t *testing.T) {
	path := os.Getenv("WA_TASK_TEST_DB")
	if path == "" {
		t.Skip("child only")
	}
	st, err := storage.New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	s := New(st.DB).ForUser(1)
	sc := Scope{UserID: 1, WorkshopID: 1, TaskID: os.Getenv("WA_TASK_TEST_ID")}
	session, err := s.EnsureSession(sc, 900001)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.EndSession(session); err != nil {
		t.Fatal(err)
	}
	session, err = s.EnsureSession(sc, 900001)
	if err != nil {
		t.Fatal(err)
	}
	task, err := s.Task(sc)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = (TaskStateMachine{s}).Apply(sc, TaskIntent{Confirmed: true, Action: "resume", Version: task.Version}); err != nil {
		t.Fatal(err)
	}
	context, err := (AgentContextBuilder{Memory: s}).Build(session, "что от меня нужно?", nil, All)
	if err != nil || !strings.Contains(context.Prompt, "[ACTIVE TASK]") || !strings.Contains(context.Prompt, "start_production") {
		t.Fatal(context.Prompt, err)
	}
}
func TestTaskFSMAtomicHistoryAndForeground(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.db")
	u, w := testkit.Owner(t, path)
	st, err := storage.New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	s := New(st.DB).ForUser(u)
	sc := Scope{UserID: u, WorkshopID: w}
	f := TaskStateMachine{s}
	a, err := f.Create(sc, TaskState{})
	if err != nil {
		t.Fatal(err)
	}
	sc.TaskID = a.ID
	if _, e := f.Create(sc, TaskState{}); !errors.Is(e, ErrForeground) {
		t.Fatal(e)
	}
	if _, err = st.DB.Exec(`CREATE TRIGGER fail_task_history BEFORE INSERT ON task_transitions BEGIN SELECT RAISE(ABORT,'test failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, e := f.Apply(sc, TaskIntent{Confirmed: true, Action: "pause", Version: a.Version}); e == nil {
		t.Fatal("history failure ignored")
	}
	current, err := s.Task(sc)
	if err != nil || current.Status != "active" || current.Version != a.Version {
		t.Fatal(current, err)
	}
	if _, err = st.DB.Exec("DROP TRIGGER fail_task_history"); err != nil {
		t.Fatal(err)
	}
	a, err = f.Apply(sc, TaskIntent{Confirmed: true, Action: "pause", Version: a.Version})
	if err != nil {
		t.Fatal(err)
	}
	b, err := f.Create(sc, TaskState{})
	if err != nil {
		t.Fatal(err)
	}
	if _, e := f.Apply(sc, TaskIntent{Confirmed: true, Action: "resume", Version: a.Version}); !errors.Is(e, ErrForeground) {
		t.Fatal(e)
	}
	sc.TaskID = b.ID
	b, err = f.Apply(sc, TaskIntent{Confirmed: true, Action: "cancel", Version: b.Version})
	if err != nil {
		t.Fatal(err)
	}
	if _, e := f.Apply(sc, TaskIntent{Confirmed: true, Action: "resume", Version: b.Version}); !errors.Is(e, ErrTransition) {
		t.Fatal(e)
	}
	var n int
	err = st.DB.QueryRow("SELECT COUNT(*) FROM production_records").Scan(&n)
	if err != nil || n != 0 {
		t.Fatal(n, err)
	}
}

func TestTaskFSMOwnershipPermissionsAndFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scopes.db")
	u, w := testkit.Owner(t, path)
	ws := workshops.NewService(path)
	defer ws.Close()
	s := New(ws.DB()).ForUser(u)
	sc := Scope{UserID: u, WorkshopID: w}
	f := TaskStateMachine{s}
	task, err := f.Create(sc, TaskState{})
	if err != nil {
		t.Fatal(err)
	}
	sc.TaskID = task.ID
	other, err := ws.UpsertUser(900002, "", "Other", "")
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := ws.CreateInvite(u, w, auth.Employee, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ws.AcceptInvite(other, token); err != nil {
		t.Fatal(err)
	}
	foreign := Scope{UserID: other, WorkshopID: w, TaskID: task.ID}
	for _, action := range []string{"pause", "resume", "cancel", "complete"} {
		if _, err = (TaskStateMachine{s.ForUser(other)}).Apply(foreign, TaskIntent{Confirmed: true, Action: action, Version: task.Version}); err == nil {
			t.Fatal("foreign action", action)
		}
	}
	if _, err = ws.DB().Exec("UPDATE workshop_members SET role='VIEWER' WHERE user_id=? AND workshop_id=?", u, w); err != nil {
		t.Fatal(err)
	}
	if _, err = f.Apply(sc, TaskIntent{Confirmed: true, Action: "pause", Version: task.Version}); !errors.Is(err, auth.ErrDenied) {
		t.Fatal(err)
	}
	if _, err = ws.DB().Exec("UPDATE workshop_members SET role='OWNER' WHERE user_id=? AND workshop_id=?", u, w); err != nil {
		t.Fatal(err)
	}
	task, err = f.Apply(sc, TaskIntent{Confirmed: true, Action: "fail", Version: task.Version})
	if err != nil || task.Status != "failed" {
		t.Fatal(task, err)
	}
	if _, err = f.Apply(sc, TaskIntent{Confirmed: true, Action: "resume", Version: task.Version}); !errors.Is(err, ErrTransition) {
		t.Fatal(err)
	}
}
