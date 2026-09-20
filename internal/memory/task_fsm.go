package memory

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"
	"workshop-agent/internal/auth"
	"workshop-agent/internal/invariants"
	"workshop-agent/internal/products"
)

var ErrTransition = errors.New("Переход недопустим: выполните текущий шаг задачи.")
var ErrTaskChanged = errors.New("Задача изменилась. Откройте /task и подтвердите текущий шаг заново.")
var ErrForeground = errors.New("Уже есть активная задача. Сначала поставьте её на паузу или отмените.")

const taskColumns = `task_id,task_type,state_json,status,user_id,workshop_id,phase,current_step,expected_action,expected_action_type,created_at,updated_at,started_at,paused_at,completed_at,version,fsm_version,COALESCE(created_by_user_id,user_id),COALESCE(assigned_to_user_id,user_id)`

type scanner interface{ Scan(...any) error }

func scanTask(row scanner) (*Task, error) {
	t := &Task{}
	var raw string
	err := row.Scan(&t.ID, &t.Type, &raw, &t.Status, &t.UserID, &t.WorkshopID, &t.Phase, &t.CurrentStep, &t.ExpectedAction, &t.ExpectedActionType, &t.CreatedAt, &t.UpdatedAt, &t.StartedAt, &t.PausedAt, &t.CompletedAt, &t.Version, &t.FSMVersion, &t.CreatedByUserID, &t.AssignedToUserID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNoTask
	}
	if err != nil {
		return nil, err
	}
	err = json.Unmarshal([]byte(raw), &t.State)
	return t, err
}
func (s *Service) Task(sc Scope) (*Task, error) {
	if err := s.authorize(s.DB, sc); err != nil {
		return nil, err
	}
	t, err := scanTask(s.DB.QueryRow("SELECT "+taskColumns+" FROM working_memory WHERE task_id=? AND workshop_id=?", sc.TaskID, sc.WorkshopID))
	if err != nil {
		return nil, err
	}
	if shared(t) {
		if err = auth.Require(s.DB, sc.UserID, sc.WorkshopID, auth.TasksRead); err != nil {
			return nil, err
		}
	} else if t.UserID != sc.UserID {
		return nil, ErrNoTask
	}
	return publicTask(t), nil
}
func (s *Service) Tasks(sc Scope) ([]*Task, error) {
	if err := s.authorize(s.DB, sc); err != nil {
		return nil, err
	}
	rows, err := s.DB.Query("SELECT "+taskColumns+" FROM working_memory WHERE COALESCE(assigned_to_user_id,user_id)=? AND workshop_id=? AND status IN ('active','waiting_input','paused') ORDER BY id DESC", sc.UserID, sc.WorkshopID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []*Task{}
	for rows.Next() {
		t, e := scanTask(rows)
		if e != nil {
			return nil, e
		}
		list = append(list, publicTask(t))
	}
	return list, rows.Err()
}

// TaskStateMachine uses the Day11 store, not a separate task repository.
type TaskStateMachine struct{ Memory *Service }

func setStep(t *Task, phase, step, expected, kind string) {
	t.Phase = phase
	t.CurrentStep = step
	t.ExpectedAction = expected
	t.ExpectedActionType = kind
}
func (f TaskStateMachine) Create(sc Scope, data TaskState) (*Task, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	t := &Task{ID: hex.EncodeToString(b), Type: "production", State: data, Status: "active", UserID: sc.UserID, WorkshopID: sc.WorkshopID, FSMVersion: 1, Version: 1}
	setStep(t, "planning", "select_product", "select_product", "USER_INPUT")
	t.CreatedByUserID = sc.UserID
	t.AssignedToUserID = sc.UserID
	if data.ProductID > 0 {
		setStep(t, "planning", "set_quantity", "confirm_quantity", "USER_INPUT")
	}
	err := f.Memory.tx(sc, func(tx *sql.Tx) error {
		if err := auth.Require(tx, sc.UserID, sc.WorkshopID, auth.TasksCreate); err != nil {
			return err
		}
		if err := validateState(tx, sc, data); err != nil {
			return err
		}
		if data.ProductID > 0 {
			if err := auth.Require(tx, sc.UserID, sc.WorkshopID, auth.ProductsRead); err != nil {
				return err
			}
			if err := tx.QueryRow("SELECT name FROM products WHERE id=? AND workshop_id=?", data.ProductID, sc.WorkshopID).Scan(&data.ProductName); err != nil {
				return err
			}
		}
		data.ProducedQuantity = nil
		data.ParametersApproved = false
		data.ValidationResult = ""
		data.ValidationReason = ""
		t.State = data
		var n int
		if err := tx.QueryRow("SELECT COUNT(*) FROM working_memory WHERE COALESCE(assigned_to_user_id,user_id)=? AND workshop_id=? AND status IN ('active','waiting_input')", sc.UserID, sc.WorkshopID).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return ErrForeground
		}
		_, err := tx.Exec(`INSERT INTO working_memory(task_id,user_id,workshop_id,task_type,state_json,status,phase,current_step,expected_action,expected_action_type,fsm_version) VALUES(?,?,?,'production',?,'active',?,?,?,?,1)`, t.ID, sc.UserID, sc.WorkshopID, compact(data), t.Phase, t.CurrentStep, t.ExpectedAction, t.ExpectedActionType)
		if err != nil {
			return err
		}
		return taskEvent(tx, sc, Task{}, *t, "created")
	})
	if err != nil {
		return nil, err
	}
	sc.TaskID = t.ID
	return f.Memory.Task(sc)
}

type TaskIntent struct {
	Confirmed bool
	Source    string
	Reason    string
	Action    string
	ProductID int64
	Quantity  *float64
	Version   int
}

func taskEvent(tx *sql.Tx, sc Scope, before, after Task, action string) error {
	return taskEventDetails(tx, sc, before, after, TaskIntent{Action: action, Source: "domain_service"})
}
func taskEventDetails(tx *sql.Tx, sc Scope, before, after Task, intent TaskIntent) error {
	action := intent.Action
	_, err := tx.Exec(`INSERT INTO task_transitions(task_id,actor_user_id,from_phase,from_step,to_phase,to_step,action,from_status,to_status,metadata_json) VALUES(?,?,?,?,?,?,?,?,?,?)`, after.ID, sc.UserID, before.Phase, before.CurrentStep, after.Phase, after.CurrentStep, action, before.Status, after.Status, compact(map[string]any{"version": after.Version, "task_data": after.State, "source": intent.Source, "result": "APPLIED", "reason": intent.Reason, "transition_key": action}))
	if err != nil {
		return err
	}
	event := "TASK_UPDATED"
	switch action {
	case "created", "created_from_draft":
		event = "TASK_CREATED"
	case "confirm_task":
		event = "TASK_CONFIRMED"
	case "start_production":
		event = "TASK_STARTED"
	case "pause":
		event = "TASK_PAUSED"
	case "resume":
		event = "TASK_RESUMED"
	case "record_result":
		event = "TASK_VALIDATION_STARTED"
	case "production_posted":
		event = "TASK_COMPLETED"
	case "cancel":
		event = "TASK_CANCELLED"
	case "fail":
		event = "TASK_FAILED"
	}
	return memoryAudit(tx, sc, event, after.ID, before, after, action)
}
func (f TaskStateMachine) Apply(sc Scope, intent TaskIntent) (*Task, error) {
	if intent.Source == "" {
		intent.Source = "internal_service"
	}
	var result *Task
	var attempted *Task
	err := f.Memory.tx(sc, func(tx *sql.Tx) error {
		t, err := scanTask(tx.QueryRow("SELECT "+taskColumns+" FROM working_memory WHERE task_id=? AND workshop_id=?", sc.TaskID, sc.WorkshopID))
		if err != nil {
			return err
		}
		if (t.FSMVersion != 1 && t.FSMVersion != 0) || (t.Type != "production" && t.Type != "assembly") {
			return errors.New("Это задача прежнего формата. Создайте новую: /task new.")
		}
		if err = TaskPermission(tx, sc.UserID, sc.WorkshopID, t); err != nil {
			return err
		}
		copy := *t
		attempted = &copy
		if intent.Version < 1 || intent.Version != t.Version {
			d := f.deny(t, intent.Action, "STALE_CONFIRMATION", ErrTaskChanged.Error()).(*TransitionDenied)
			d.cause = ErrTaskChanged
			return d
		}
		if intent.Action != "pause" && intent.Action != "cancel" && intent.Action != "fail" {
			if err = auth.Require(tx, t.AssignedToUserID, sc.WorkshopID, auth.TasksExecute); err != nil {
				return err
			}
		}
		if t.Status == "completed" || t.Status == "cancelled" || t.Status == "failed" || t.Phase == "done" {
			return f.deny(t, intent.Action, "TERMINAL_STATE", "Задача завершена или отменена. Обычное продолжение запрещено.")
		}
		before := *t
		// Adopt an existing legacy task in place, only on an explicit lifecycle action.
		// Planning data is preserved; no execution or domain operation is inferred.
		if t.FSMVersion == 0 {
			if intent.Action != "pause" && intent.Action != "resume" && intent.Action != "cancel" && intent.Action != "adopt_task" {
				return ErrTransition
			}
			t.FSMVersion = 1
			setStep(t, "planning", "select_product", "select_product", "USER_INPUT")
			if t.State.ProductID > 0 {
				setStep(t, "planning", "set_quantity", "confirm_quantity", "USER_INPUT")
				if t.State.Quantity > 0 && math.Trunc(t.State.Quantity) == t.State.Quantity {
					setStep(t, "planning", "confirm_task", "confirm_task", "USER_CONFIRMATION")
				}
			}
		}
		if intent.Action == "complete" {
			if t.Phase == "validation" && t.CurrentStep == "confirm_completion" {
				return ErrPostingRequired
			}
			return f.deny(t, intent.Action, "INVALID_TRANSITION", "Сначала выполните задачу и проверьте результат. Прямое завершение запрещено.")
		}
		if err = f.guard(tx, sc, t, intent, false); err != nil {
			return err
		}
		now := time.Now().UTC().Format(time.RFC3339Nano)
		switch intent.Action {
		case "adopt_task":
			if before.FSMVersion != 0 {
				return ErrTransition
			}
		case "pause":
			if t.Status != "active" {
				return ErrTransition
			}
			t.Status = "paused"
			t.PausedAt = &now
		case "resume":
			if err = auth.Require(tx, t.AssignedToUserID, sc.WorkshopID, auth.TasksExecute); err != nil {
				return err
			}
			if t.Status != "paused" {
				return ErrTransition
			}
			var n int
			if err = tx.QueryRow("SELECT COUNT(*) FROM working_memory WHERE COALESCE(assigned_to_user_id,user_id)=? AND workshop_id=? AND status IN ('active','waiting_input')", t.AssignedToUserID, sc.WorkshopID).Scan(&n); err != nil {
				return err
			}
			if n > 0 {
				return ErrForeground
			}
			t.Status = "active"
		case "cancel", "fail":
			t.Status = "cancelled"
			t.ExpectedAction = "none"
			t.ExpectedActionType = "NONE"
			if intent.Action == "fail" {
				t.Status = "failed"
			}
			t.CompletedAt = &now
		default:
			if t.Status != "active" {
				return ErrTransition
			}
			switch intent.Action {
			case "edit_parameters":
				t.State.ParametersApproved = false
				setStep(t, "planning", "select_product", "select_product", "USER_INPUT")
			case "select_product":
				if t.Phase != "planning" || t.CurrentStep != "select_product" {
					return ErrTransition
				}
				if err = auth.Require(tx, sc.UserID, sc.WorkshopID, auth.ProductsRead); err != nil {
					return err
				}
				if err = tx.QueryRow("SELECT name FROM products WHERE id=? AND workshop_id=?", intent.ProductID, sc.WorkshopID).Scan(&t.State.ProductName); err != nil {
					return ErrScope
				}
				t.State.ProductID = intent.ProductID
				setStep(t, "planning", "set_quantity", "confirm_quantity", "USER_INPUT")
			case "set_quantity":
				if t.Phase != "planning" || (t.CurrentStep != "set_quantity" && t.CurrentStep != "confirm_task") || intent.Quantity == nil || *intent.Quantity <= 0 || math.Trunc(*intent.Quantity) != *intent.Quantity {
					return ErrTransition
				}
				t.State.Quantity = *intent.Quantity
				t.State.ParametersApproved = false
				setStep(t, "planning", "confirm_task", "confirm_task", "USER_CONFIRMATION")
			case "confirm_task":
				if t.Phase != "planning" || t.CurrentStep != "confirm_task" || t.State.ProductID <= 0 || t.State.Quantity <= 0 {
					return ErrTransition
				}
				t.State.ParametersApproved = true
				setStep(t, "execution", "start_production", "start_production", "USER_CONFIRMATION")
				t.StartedAt = &now
			case "start_production":
				if t.Phase != "execution" || t.CurrentStep != "start_production" {
					return ErrTransition
				}
				rules, e := (invariants.InvariantRegistry{}).Workshop(tx, sc.UserID, sc.WorkshopID)
				if e != nil {
					return e
				}
				if rules[len(rules)-1].IsActive {
					calculation, e := products.CalculateProductionTx(tx, sc.UserID, sc.WorkshopID, t.State.ProductID, t.State.Quantity)
					if e != nil {
						return e
					}
					if e = invariants.Check(tx, invariants.ProposedAction{ActionType: "start_production", UserID: sc.UserID, WorkshopID: sc.WorkshopID, TaskID: t.ID}, invariants.Facts{MaterialShortage: calculation.Shortage()}); e != nil {
						return e
					}
				}
				setStep(t, "execution", "record_result", "user_input_produced_quantity", "USER_INPUT")
			case "record_result":
				if t.Phase != "execution" || t.CurrentStep != "record_result" || intent.Quantity == nil || *intent.Quantity < 0 || *intent.Quantity > 1e9 || math.IsNaN(*intent.Quantity) || math.IsInf(*intent.Quantity, 0) || math.Trunc(*intent.Quantity) != *intent.Quantity {
					return ErrTransition
				}
				t.State.ProducedQuantity = intent.Quantity
				t.State.ValidationResult = ""
				setStep(t, "validation", "verify_result", "verify_result", "USER_CONFIRMATION")
			case "verify_result":
				if t.Phase != "validation" || t.CurrentStep != "verify_result" || t.State.ProducedQuantity == nil {
					return ErrTransition
				}
				setStep(t, "validation", "confirm_completion", "confirm_completion", "USER_CONFIRMATION")
				t.State.ValidationResult = "passed"
			case "complete":
				if t.Phase != "validation" || t.CurrentStep != "confirm_completion" || t.State.ProducedQuantity == nil {
					return ErrTransition
				}
				return ErrPostingRequired
			case "correct_result":
				if t.Phase != "validation" {
					return ErrTransition
				}
				t.State.ProducedQuantity = nil
				t.State.ValidationResult = "failed"
				t.State.ValidationReason = intent.Reason
				setStep(t, "execution", "record_result", "user_input_produced_quantity", "USER_INPUT")
			default:
				return ErrTransition
			}
		}
		if err = validateState(tx, sc, t.State); err != nil {
			return err
		}
		if err = f.saveTransition(tx, sc, before, t, intent); err != nil {
			return err
		}
		result = publicTask(t)
		return nil
	})
	if err == ErrTransition && attempted != nil {
		err = f.deny(attempted, intent.Action, "PRECONDITION_FAILED", "Не выполнены условия текущего шага: проверьте товар, количество и результат.")
	}
	if err != nil {
		f.recordDenial(sc, intent, err)
	}
	return result, err
}
func (s *Service) TaskHistory(sc Scope) ([]map[string]any, error) {
	if _, err := s.Task(sc); err != nil {
		return nil, err
	}
	rows, err := s.DB.Query(`SELECT from_phase,from_step,to_phase,to_step,action,from_status,to_status,created_at,actor_user_id,metadata_json FROM task_transitions WHERE task_id=? ORDER BY id`, sc.TaskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var fp, fs, tp, ts, a, fst, tst, at string
		var actor int64
		var raw string
		var metadata map[string]any
		if err = rows.Scan(&fp, &fs, &tp, &ts, &a, &fst, &tst, &at, &actor, &raw); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(raw), &metadata)
		out = append(out, map[string]any{"task_id": sc.TaskID, "from": fp + ":" + fs, "to": tp + ":" + ts, "action": a, "transition_key": a, "from_phase": fp, "to_phase": tp, "from_status": fst, "to_status": tst, "status": fst + " → " + tst, "at": at, "actor_user_id": actor, "source": metadata["source"], "result": metadata["result"], "reason": metadata["reason"]})
	}
	return out, rows.Err()
}
func (t Task) CompactState() string {
	if shared(&t) {
		t = *publicTask(&t)
	}
	return fmt.Sprintf("[ACTIVE TASK]\nTask ID: %s\nType: %s\nPhase: %s\nCurrent step: %s\nExpected action: %s (%s)\nStatus: %s\nTask data: %s", t.ID, t.Type, t.Phase, t.CurrentStep, t.ExpectedAction, t.ExpectedActionType, t.Status, compact(t.State))
}
