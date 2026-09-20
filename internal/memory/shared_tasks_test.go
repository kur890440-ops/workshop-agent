package memory

import (
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"workshop-agent/internal/auth"
	"workshop-agent/internal/testkit"
	"workshop-agent/internal/workshops"
)

func TestSharedTasksAssignmentPermissionsAndPrivacy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shared.db")
	owner, w := testkit.Owner(t, path)
	ws := workshops.NewService(path)
	defer ws.Close()
	member := func(telegram int64, role auth.Role) int64 {
		u, e := ws.UpsertUser(telegram, "", "Employee", "")
		if e != nil {
			t.Fatal(e)
		}
		_, token, e := ws.CreateInvite(owner, w, role, 0, 0)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = ws.AcceptInvite(u, token); e != nil {
			t.Fatal(e)
		}
		return u
	}
	employee := member(920001, auth.Employee)
	viewer := member(920002, auth.Viewer)
	admin := member(920003, auth.Admin)
	m := New(ws.DB()).ForUser(owner)
	sc := Scope{UserID: owner, WorkshopID: w}
	f := TaskStateMachine{m}
	task, err := f.Create(sc, TaskState{Parameters: map[string]string{"secret": "PRIVATE NOTE"}, OpenQuestions: []string{"PRIVATE QUESTION"}})
	if err != nil {
		t.Fatal(err)
	}
	sc.TaskID = task.ID
	task, err = m.AssignTask(sc, employee, task.Version)
	if err != nil {
		t.Fatal(err)
	}
	if task.CreatedByUserID != owner || task.AssignedToUserID != employee {
		t.Fatal(task)
	}
	for _, u := range []int64{owner, employee, viewer, admin} {
		list, e := m.ForUser(u).WorkshopTasks(Scope{UserID: u, WorkshopID: w}, false)
		if e != nil || len(list) != 1 {
			t.Fatal(list, e)
		}
		if strings.Contains(compact(list), "PRIVATE") {
			t.Fatal("private state exposed")
		}
	}
	if active, e := m.ActiveWorking(Scope{UserID: owner, WorkshopID: w}); e != nil || active != nil {
		t.Fatal(active, e)
	}
	esc := Scope{UserID: employee, WorkshopID: w, TaskID: task.ID}
	em := m.ForUser(employee)
	if active, e := em.ActiveWorking(esc); e != nil || active == nil || active.ID != task.ID {
		t.Fatal(active, e)
	}
	if _, e := em.AssignTask(esc, owner, task.Version); !errors.Is(e, auth.ErrDenied) {
		t.Fatal(e)
	}
	if _, e := (TaskStateMachine{em}).Create(Scope{UserID: employee, WorkshopID: w}, TaskState{}); !errors.Is(e, auth.ErrDenied) {
		t.Fatal(e)
	}
	vsc := Scope{UserID: viewer, WorkshopID: w, TaskID: task.ID}
	if _, e := (TaskStateMachine{m.ForUser(viewer)}).Apply(vsc, TaskIntent{Confirmed: true, Action: "pause", Version: task.Version}); !errors.Is(e, auth.ErrDenied) {
		t.Fatal(e)
	}
	task, err = (TaskStateMachine{em}).Apply(esc, TaskIntent{Confirmed: true, Action: "pause", Version: task.Version})
	if err != nil {
		t.Fatal(err)
	}
	oldVersion := task.Version
	task, err = m.AssignTask(sc, admin, task.Version)
	if err != nil {
		t.Fatal(err)
	}
	if _, e := (TaskStateMachine{em}).Apply(esc, TaskIntent{Confirmed: true, Action: "resume", Version: oldVersion}); e == nil {
		t.Fatal("former executor changed task")
	}
	if _, e := m.AssignTask(sc, employee, oldVersion); !errors.Is(e, ErrTaskChanged) {
		t.Fatal(e)
	}
	var raw string
	if e := ws.DB().QueryRow("SELECT state_json FROM working_memory WHERE task_id=?", task.ID).Scan(&raw); e != nil || !strings.Contains(raw, "PRIVATE NOTE") {
		t.Fatal(raw, e)
	}
	// Admin performs FSM transition; audit records the actor, not the creator.
	asc := Scope{UserID: admin, WorkshopID: w, TaskID: task.ID}
	task, err = (TaskStateMachine{m.ForUser(admin)}).Apply(asc, TaskIntent{Confirmed: true, Action: "resume", Version: task.Version})
	if err != nil {
		t.Fatal(err)
	}
	var actor int64
	if e := ws.DB().QueryRow("SELECT actor_user_id FROM task_transitions WHERE task_id=? ORDER BY id DESC LIMIT 1", task.ID).Scan(&actor); e != nil || actor != admin {
		t.Fatal(actor, e)
	}
	// Removed executor remains a historical reference, but loses access.
	if _, e := ws.DB().Exec("UPDATE workshop_members SET status='removed',is_active=0 WHERE user_id=? AND workshop_id=?", admin, w); e != nil {
		t.Fatal(e)
	}
	if _, e := m.ForUser(admin).WorkshopTasks(asc, false); e == nil {
		t.Fatal("removed member can read")
	}
	task, err = m.AssignTask(sc, employee, task.Version)
	if err != nil {
		t.Fatal(err)
	}
	if task.ID != sc.TaskID || task.State.Quantity != 0 {
		t.Fatal(task)
	}
	other, e := ws.CreateOwnedWorkshop(owner, "Other")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = m.Task(sc); e == nil {
		t.Fatal("old workshop allowed")
	}
	if _, e = m.Task(Scope{UserID: owner, WorkshopID: other, TaskID: task.ID}); e == nil {
		t.Fatal("cross workshop allowed")
	}
}

func TestSharedTaskConcurrentAssignmentAndConflict(t *testing.T) {
	path := filepath.Join(t.TempDir(), "concurrent.db")
	u, w := testkit.Owner(t, path)
	ws := workshops.NewService(path)
	defer ws.Close()
	m := New(ws.DB()).ForUser(u)
	sc := Scope{UserID: u, WorkshopID: w}
	f := TaskStateMachine{m}
	a, e := f.Create(sc, TaskState{})
	if e != nil {
		t.Fatal(e)
	}
	sc.TaskID = a.ID
	a, e = f.Apply(sc, TaskIntent{Confirmed: true, Action: "pause", Version: a.Version})
	if e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := m.AssignTask(sc, u, a.Version); results <- err }()
	}
	wg.Wait()
	close(results)
	success := 0
	for e := range results {
		if e == nil {
			success++
		} else if !errors.Is(e, ErrTaskChanged) {
			t.Fatal(e)
		}
	}
	if success != 1 {
		t.Fatal(success)
	}
	if _, e = f.Create(Scope{UserID: u, WorkshopID: w}, TaskState{}); e != nil {
		t.Fatal(e)
	}
	a, e = m.Task(sc)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = m.AssignTask(sc, u, a.Version); !errors.Is(e, ErrForeground) {
		t.Fatal(e)
	}
}
