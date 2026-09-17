package memory

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"
	"workshop-agent/internal/auth"
	"workshop-agent/internal/products"
)

var ErrPostingRequired = errors.New("Для завершения требуется проверка выпуска и подтверждение «Завершить и списать».")

type Completion struct {
	Token string
	Plan  products.ProductionPlan
	Task  *Task
}

func completionTask(tx *sql.Tx, sc Scope) (*Task, error) {
	t, e := scanTask(tx.QueryRow("SELECT "+taskColumns+" FROM working_memory WHERE task_id=? AND workshop_id=?", sc.TaskID, sc.WorkshopID))
	if e != nil {
		return nil, e
	}
	if !shared(t) {
		return nil, ErrTransition
	}
	if e = TaskPermission(tx, sc.UserID, sc.WorkshopID, t); e != nil {
		return nil, e
	}
	for _, p := range []auth.Permission{auth.ProductionCreate, auth.InventoryWrite} {
		if e = auth.Require(tx, sc.UserID, sc.WorkshopID, p); e != nil {
			return nil, e
		}
	}
	return t, nil
}
func canPost(t *Task) bool {
	return t.FSMVersion == 1 && t.Status == "active" && ((t.Phase == "execution" && t.CurrentStep == "record_result") || (t.Phase == "validation" && (t.CurrentStep == "verify_result" || t.CurrentStep == "confirm_completion")))
}
func receipt(tx *sql.Tx, sc Scope) (products.ProductionPlan, error) {
	var p products.ProductionPlan
	var raw string
	e := tx.QueryRow("SELECT composition_json FROM production_records WHERE task_id=? AND workshop_id=?", sc.TaskID, sc.WorkshopID).Scan(&raw)
	if e != nil {
		return p, e
	}
	e = json.Unmarshal([]byte(raw), &p)
	return p, e
}
func (s *Service) CompletionReceipt(sc Scope) (products.ProductionPlan, error) {
	var p products.ProductionPlan
	e := s.tx(sc, func(tx *sql.Tx) error {
		if _, e := completionTask(tx, sc); e != nil {
			return e
		}
		var e error
		p, e = receipt(tx, sc)
		return e
	})
	return p, e
}
func (s *Service) PrepareCompletion(sc Scope, chat int64, qty float64) (Completion, error) {
	var out Completion
	b := make([]byte, 24)
	if _, e := rand.Read(b); e != nil {
		return out, e
	}
	out.Token = hex.EncodeToString(b)
	e := s.tx(sc, func(tx *sql.Tx) error {
		t, e := completionTask(tx, sc)
		if e != nil {
			return e
		}
		out.Task = publicTask(t)
		if t.Status == "completed" {
			out.Plan, e = receipt(tx, sc)
			return e
		}
		if !canPost(t) {
			return ErrTransition
		}
		if e = session(tx, sc); e != nil {
			return e
		}
		if e = auth.Require(tx, t.AssignedToUserID, sc.WorkshopID, auth.TasksExecute); e != nil {
			return e
		}
		out.Plan, e = products.CalculateProductionTx(tx, sc.UserID, sc.WorkshopID, t.State.ProductID, qty)
		if e != nil {
			return e
		}
		_, e = tx.Exec("UPDATE task_completion_intents SET expires_at=0 WHERE user_id=? AND workshop_id=? AND chat_id=? AND consumed=0", sc.UserID, sc.WorkshopID, chat)
		if e != nil {
			return e
		}
		_, e = tx.Exec("INSERT INTO task_completion_intents(id,user_id,workshop_id,chat_id,session_id,task_id,version,plan_json,expires_at) VALUES(?,?,?,?,?,?,?,?,?)", out.Token, sc.UserID, sc.WorkshopID, chat, sc.SessionID, t.ID, t.Version, compact(out.Plan), time.Now().Add(15*time.Minute).Unix())
		return e
	})
	return out, e
}
func (s *Service) CancelCompletion(sc Scope, chat int64) error {
	return s.tx(sc, func(tx *sql.Tx) error {
		_, e := tx.Exec("UPDATE task_completion_intents SET expires_at=0 WHERE user_id=? AND workshop_id=? AND chat_id=? AND consumed=0", sc.UserID, sc.WorkshopID, chat)
		return e
	})
}
func (s *Service) PostCompletion(sc Scope, chat int64, token string) (products.ProductionPlan, error) {
	var out products.ProductionPlan
	e := s.tx(sc, func(tx *sql.Tx) error {
		var version int
		var sessionID, expires int64
		var raw string
		if e := tx.QueryRow("SELECT task_id,version,session_id,plan_json,expires_at FROM task_completion_intents WHERE id=? AND user_id=? AND workshop_id=? AND chat_id=?", token, sc.UserID, sc.WorkshopID, chat).Scan(&sc.TaskID, &version, &sessionID, &raw, &expires); e != nil {
			return e
		}
		t, e := completionTask(tx, sc)
		if e != nil {
			return e
		}
		if t.Status == "completed" {
			out, e = receipt(tx, sc)
			return e
		}
		if expires <= time.Now().Unix() || sessionID != sc.SessionID {
			return ErrTaskChanged
		}
		if e = session(tx, sc); e != nil {
			return e
		}
		if !canPost(t) || t.Version != version {
			return ErrTaskChanged
		}
		if e = auth.Require(tx, t.AssignedToUserID, sc.WorkshopID, auth.TasksExecute); e != nil {
			return e
		}
		var plan products.ProductionPlan
		if e = json.Unmarshal([]byte(raw), &plan); e != nil {
			return e
		}
		if plan.ProductID != t.State.ProductID {
			return ErrTaskChanged
		}
		out, e = products.PostProductionTx(tx, sc.UserID, sc.WorkshopID, t.CreatedByUserID, t.AssignedToUserID, t.ID, plan)
		if e != nil {
			return e
		}
		before := *publicTask(t)
		t.State.ProducedQuantity = &out.Quantity
		t.Status = "completed"
		setStep(t, "done", "completed", "none", "NONE")
		t.Version++
		now := time.Now().UTC().Format(time.RFC3339Nano)
		t.CompletedAt = &now
		if _, e = tx.Exec("UPDATE working_memory SET state_json=?,status='completed',phase='done',current_step='completed',expected_action='none',expected_action_type='NONE',version=?,completed_at=?,updated_at=? WHERE task_id=? AND workshop_id=?", compact(t.State), t.Version, now, now, t.ID, sc.WorkshopID); e != nil {
			return e
		}
		if e = taskEvent(tx, sc, before, *publicTask(t), "production_posted"); e != nil {
			return e
		}
		_, e = tx.Exec("UPDATE task_completion_intents SET consumed=1 WHERE id=?", token)
		return e
	})
	return out, e
}
