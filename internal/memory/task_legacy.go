package memory

import (
	"database/sql"
	"errors"
	"time"
)

// Compatibility for stored FSM version 0; never authorizes phase changes or production posting.
func (f TaskStateMachine) legacyUpdateWorkingMemory(sc Scope, state TaskState, status string) error {
	s := f.Memory
	if status != "active" && status != "waiting_input" {
		return errors.New("invalid active task status")
	}
	return s.tx(sc, func(tx *sql.Tx) error {
		if err := s.legacyTaskPermission(tx, sc); err != nil {
			return err
		}
		if err := preservePrivateTaskState(tx, sc, &state); err != nil {
			return err
		}
		if err := validateState(tx, sc, state); err != nil {
			return err
		}
		t, err := scanTask(tx.QueryRow("SELECT "+taskColumns+" FROM working_memory WHERE task_id=? AND workshop_id=?", sc.TaskID, sc.WorkshopID))
		if err != nil {
			return err
		}
		if t.FSMVersion != 0 || t.UserID != sc.UserID || t.AssignedToUserID != t.UserID {
			return ErrNoTask
		}
		before := *t
		state.ParametersApproved = false
		state.ValidationResult = ""
		t.State = state
		t.Status = status
		return f.saveTransition(tx, sc, before, t, TaskIntent{Action: "legacy_update", Source: "legacy_memory_api"})
	})
}

func (f TaskStateMachine) legacyCompleteWorkingMemory(sc Scope, cancel bool) error {
	s := f.Memory
	t, err := s.Task(sc)
	if err != nil {
		return err
	}
	if shared(t) && cancel {
		_, e := f.Apply(sc, TaskIntent{Action: "cancel", Version: t.Version, Source: "legacy_memory_api"})
		return e
	}
	if !cancel {
		t, e := s.Task(sc)
		if e != nil {
			return e
		}
		if shared(t) {
			return ErrPostingRequired
		}
	}
	status := "completed"
	if cancel {
		status = "cancelled"
	}
	return s.tx(sc, func(tx *sql.Tx) error {
		if err := s.legacyTaskPermission(tx, sc); err != nil {
			return err
		}
		t, e := scanTask(tx.QueryRow("SELECT "+taskColumns+" FROM working_memory WHERE task_id=? AND workshop_id=?", sc.TaskID, sc.WorkshopID))
		if e != nil {
			return e
		}
		if t.FSMVersion != 0 || shared(t) || t.UserID != sc.UserID {
			return ErrNoTask
		}
		before := *t
		t.Status = status
		action := "legacy_cancel"
		if !cancel {
			action = "legacy_complete"
		}
		t.ExpectedAction = "none"
		t.ExpectedActionType = "NONE"
		now := time.Now().UTC().Format(time.RFC3339Nano)
		t.CompletedAt = &now
		return f.saveTransition(tx, sc, before, t, TaskIntent{Action: action, Source: "legacy_memory_api"})
	})
}
