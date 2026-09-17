package memory

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"math"
	"time"
	"workshop-agent/internal/auth"
)

var ErrActivePlan = errors.New("Уже есть активная задача. Посмотрите /task; завершите её через /task complete или отмените через /task cancel, затем повторите сборку.")

// ConfirmAssemblyDraft consumes the pending intent and creates an ordinary
// working task atomically. It never writes inventory or production tables.
func (s *Service) ConfirmAssemblyDraft(sc Scope, chat, draftID int64, state TaskState) error {
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	return s.tx(sc, func(tx *sql.Tx) error {
		for _, p := range []auth.Permission{auth.PlanningWrite, auth.ProductsRead, auth.BOMRead, auth.InventoryRead} {
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
		if err := tx.QueryRow("SELECT COUNT(*) FROM working_memory WHERE user_id=? AND workshop_id=? AND status IN ('active','waiting_input')", sc.UserID, sc.WorkshopID).Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			return ErrActivePlan
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
		if _, err := tx.Exec("INSERT INTO working_memory(user_id,workshop_id,task_id,task_type,state_json,status) VALUES(?,?,?,'assembly',?,'active')", sc.UserID, sc.WorkshopID, hex.EncodeToString(nonce), compact(state)); err != nil {
			return err
		}
		if _, err := tx.Exec("DELETE FROM pending_actions WHERE id=?", draftID); err != nil {
			return err
		}
		return memoryAudit(tx, sc, "ASSEMBLY_PLAN_CREATED", "assembly", nil, state, "confirmed_draft")
	})
}
