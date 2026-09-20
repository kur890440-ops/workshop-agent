package memory

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"time"
	"workshop-agent/internal/auth"
)

var ErrActiveTask = errors.New("Уже есть активная задача. Посмотрите /task; завершите её через /task complete или отмените через /task cancel, затем повторите сборку.")

// ConfirmAssemblyDraft consumes the pending intent and creates an ordinary
// working task atomically. It never writes inventory or production tables.
func (s *Service) ConfirmAssemblyDraft(sc Scope, chat, draftID int64, state TaskState, revision ...int) error {
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	return s.tx(sc, func(tx *sql.Tx) error {
		for _, p := range []auth.Permission{auth.TasksCreate, auth.ProductsRead, auth.BOMRead, auth.InventoryRead} {
			if err := auth.Require(tx, sc.UserID, sc.WorkshopID, p); err != nil {
				return err
			}
		}
		if err := session(tx, sc); err != nil {
			return err
		}
		var n int
		if err := tx.QueryRow("SELECT COUNT(*) FROM pending_actions WHERE id=? AND user_id=? AND workshop_id=? AND chat_id=? AND action_type='assembly_draft' AND expires_at>?", draftID, sc.UserID, sc.WorkshopID, chat, time.Now().UTC().Format(time.RFC3339)).Scan(&n); err != nil {
			return err
		}
		if n != 1 {
			return errors.New("Черновик устарел. Начните сборку заново.")
		}
		var raw string
		if err := tx.QueryRow("SELECT payload FROM pending_actions WHERE id=?", draftID).Scan(&raw); err != nil {
			return err
		}
		var draft struct {
			Assignee int64
			Product  int64
			Quantity int64
			Session  int64
			Stage    string
			Revision int
		}
		if err := json.Unmarshal([]byte(raw), &draft); err != nil {
			return err
		}
		if draft.Product != state.ProductID || float64(draft.Quantity) != state.Quantity || draft.Session != sc.SessionID || draft.Stage != "confirm" || (len(revision) > 0 && revision[0] != draft.Revision) {
			return ErrTaskChanged
		}
		if draft.Assignee == 0 {
			draft.Assignee = sc.UserID
		}
		if err := assignmentAllowed(tx, sc, draft.Assignee); err != nil {
			return err
		}
		if err := tx.QueryRow("SELECT COUNT(*) FROM working_memory WHERE COALESCE(assigned_to_user_id,user_id)=? AND workshop_id=? AND status IN ('active','waiting_input')", draft.Assignee, sc.WorkshopID).Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			return ErrActiveTask
		}
		if state.Quantity <= 0 || state.Quantity > 1e9 || math.Trunc(state.Quantity) != state.Quantity {
			return errors.New("Некорректное количество изделий")
		}
		if err := validateState(tx, sc, state); err != nil {
			return err
		}
		if err := tx.QueryRow("SELECT name FROM products WHERE id=? AND workshop_id=?", state.ProductID, sc.WorkshopID).Scan(&state.ProductName); err != nil {
			return err
		}
		id := hex.EncodeToString(nonce)
		if _, err := tx.Exec("INSERT INTO working_memory(user_id,workshop_id,task_id,task_type,state_json,status,phase,current_step,expected_action,expected_action_type,fsm_version,created_by_user_id,assigned_to_user_id) VALUES(?,?,?,'assembly',?,'active','planning','confirm_task','confirm_task','USER_CONFIRMATION',1,?,?)", sc.UserID, sc.WorkshopID, id, compact(state), sc.UserID, draft.Assignee); err != nil {
			return err
		}
		if _, err := tx.Exec("DELETE FROM pending_actions WHERE id=?", draftID); err != nil {
			return err
		}
		task := Task{CreatedByUserID: sc.UserID, AssignedToUserID: draft.Assignee, ID: id, Type: "assembly", UserID: sc.UserID, WorkshopID: sc.WorkshopID, State: state, Status: "active", Phase: "planning", CurrentStep: "confirm_task", ExpectedAction: "confirm_task", ExpectedActionType: "USER_CONFIRMATION", Version: 1, FSMVersion: 1}
		if err := taskEvent(tx, sc, Task{}, task, "created_from_draft"); err != nil {
			return err
		}
		return nil
	})
}
