package memory

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"workshop-agent/internal/auth"
	"workshop-agent/internal/invariants"
)

// TransitionDefinition describes the existing FSM; it is not a second engine.
// A '*' destination preserves the current phase/step (lifecycle-only action).
type TransitionDefinition struct {
	ToStatus             string            `json:"to_status"`
	Key                  string            `json:"key"`
	Label                string            `json:"label"`
	FromPhase            string            `json:"from_phase"`
	FromStep             string            `json:"from_step"`
	FromStatus           string            `json:"from_status"`
	ToPhase              string            `json:"to_phase"`
	ToStep               string            `json:"to_step"`
	RequiresConfirmation bool              `json:"requires_confirmation"`
	Preconditions        []string          `json:"preconditions"`
	Invariants           []string          `json:"invariants"`
	RequiredPermissions  []auth.Permission `json:"required_permissions"`
}

func TransitionDefinitions() []TransitionDefinition {
	definitions := []TransitionDefinition{
		{"*", "select_product", "Выбрать товар", "planning", "select_product", "active", "planning", "set_quantity", false, []string{"product_in_workshop"}, nil, []auth.Permission{auth.ProductsRead}},
		{"*", "set_quantity", "Указать количество", "planning", "set_quantity,confirm_task", "active", "planning", "confirm_task", false, []string{"positive_integer_quantity"}, nil, nil},
		{"*", "edit_parameters", "Изменить параметры", "planning", "confirm_task", "active", "planning", "select_product", false, nil, nil, nil},
		{"*", "confirm_task", "Утвердить параметры задачи", "planning", "confirm_task", "active", "execution", "start_production", true, []string{"product_selected", "quantity_set"}, nil, nil},
		{"*", "start_production", "Начать выполнение", "execution", "start_production", "active", "execution", "record_result", true, []string{"parameters_approved"}, []string{"requires_material_check_before_production"}, nil},
		{"*", "record_result", "Указать результат выполнения", "execution", "record_result", "active", "validation", "verify_result", false, []string{"execution_result"}, nil, nil},
		{"*", "verify_result", "Подтвердить проверку результата", "validation", "verify_result", "active", "validation", "confirm_completion", true, []string{"execution_result"}, nil, nil},
		{"*", "correct_result", "Вернуть на доработку", "validation", "*", "active", "execution", "record_result", false, []string{"return_reason"}, nil, nil},
		{"completed", "production_posted", "Завершить и списать", "validation", "confirm_completion", "active", "done", "completed", true, []string{"validation_passed", "confirmed_posting"}, []string{"validation_before_completion"}, []auth.Permission{auth.ProductionCreate, auth.InventoryWrite}},
		{"paused", "pause", "Поставить на паузу", "planning,execution,validation", "*", "active", "*", "*", false, nil, nil, nil},
		{"active", "resume", "Продолжить", "planning,execution,validation", "*", "paused", "*", "*", false, []string{"no_other_active_task"}, nil, nil},
		{"cancelled", "cancel", "Отменить задачу", "planning,execution,validation", "*", "active,paused,waiting_input", "*", "*", false, nil, nil, nil},
		{"failed", "fail", "Зафиксировать невозможность выполнения", "planning,execution,validation", "*", "active,paused", "*", "*", false, nil, nil, nil},
		{"*", "adopt_task", "Подготовить старую задачу", "planning", "*", "active,paused", "planning", "select_product,set_quantity,confirm_task", true, []string{"legacy_task"}, nil, nil},
		{"active,waiting_input", "legacy_update", "Изменить параметры старого формата", "planning", "legacy", "active,waiting_input", "planning", "legacy", false, []string{"fsm_version_zero"}, nil, nil},
		{"completed", "legacy_complete", "Закрыть личную заметку старого формата", "planning", "legacy", "active,waiting_input", "planning", "legacy", false, []string{"fsm_version_zero", "personal_task_only"}, nil, nil},
		{"cancelled", "legacy_cancel", "Отменить личную заметку старого формата", "planning", "legacy", "active,waiting_input", "planning", "legacy", false, []string{"fsm_version_zero", "personal_task_only"}, nil, nil},
	}
	for i := range definitions {
		if !strings.HasPrefix(definitions[i].Key, "legacy_") {
			definitions[i].RequiredPermissions = append([]auth.Permission{auth.TasksExecute}, definitions[i].RequiredPermissions...)
		}
		definitions[i].Invariants = append([]string{"authorized_workshop"}, definitions[i].Invariants...)
	}
	return definitions
}
func matches(rule, value string) bool {
	return rule == "*" || strings.Contains(","+rule+",", ","+value+",")
}
func transitionFor(t *Task, key string) (TransitionDefinition, bool) {
	if t.Phase == "done" || t.Status == "completed" || t.Status == "cancelled" || t.Status == "failed" {
		return TransitionDefinition{}, false
	}
	for _, d := range TransitionDefinitions() {
		if d.Key == key && matches(d.FromPhase, t.Phase) && matches(d.FromStep, t.CurrentStep) && matches(d.FromStatus, t.Status) {
			return d, true
		}
	}
	return TransitionDefinition{}, false
}

// GetAllowedTransitions lists structurally applicable requests; payload-dependent
// conditions are rechecked on Apply under the transaction's write lock.
func (f TaskStateMachine) GetAllowedTransitions(t *Task) []TransitionDefinition {
	out := []TransitionDefinition{}
	for _, d := range TransitionDefinitions() {
		if strings.HasPrefix(d.Key, "legacy_") {
			continue
		}
		if _, ok := transitionFor(t, d.Key); !ok {
			continue
		}
		if d.Key == "adopt_task" && t.FSMVersion != 0 {
			continue
		}
		if t.FSMVersion == 0 && d.Key != "adopt_task" && d.Key != "pause" && d.Key != "resume" && d.Key != "cancel" {
			continue
		}
		if d.Key == "confirm_task" && (t.State.ProductID <= 0 || t.State.Quantity <= 0) {
			continue
		}
		if d.Key == "start_production" && !t.State.ParametersApproved {
			continue
		}
		if d.Key == "verify_result" && t.State.ProducedQuantity == nil {
			continue
		}
		if d.Key == "production_posted" && t.State.ValidationResult != "passed" {
			continue
		}
		out = append(out, d)
	}
	return out
}
func (f TaskStateMachine) AllowedTransitions(sc Scope, t *Task) []TransitionDefinition {
	if f.Memory == nil || TaskPermission(f.Memory.DB, sc.UserID, sc.WorkshopID, t) != nil {
		return nil
	}
	var out []TransitionDefinition
	for _, d := range f.GetAllowedTransitions(t) {
		valid := true
		if d.Key == "resume" {
			var n int
			e := f.Memory.DB.QueryRow("SELECT COUNT(*) FROM working_memory WHERE COALESCE(assigned_to_user_id,user_id)=? AND workshop_id=? AND status IN ('active','waiting_input')", t.AssignedToUserID, sc.WorkshopID).Scan(&n)
			if e != nil || n > 0 {
				valid = false
			}
		}
		for _, p := range d.RequiredPermissions {
			if auth.Require(f.Memory.DB, sc.UserID, sc.WorkshopID, p) != nil {
				valid = false
			}
		}
		if d.Key != "pause" && d.Key != "cancel" && d.Key != "fail" && auth.Require(f.Memory.DB, t.AssignedToUserID, sc.WorkshopID, auth.TasksExecute) != nil {
			valid = false
		}
		if valid {
			out = append(out, d)
		}
	}
	return out
}

type TransitionRequest struct {
	TaskID      string     `json:"task_id"`
	ActorUserID int64      `json:"actor_user_id"`
	Transition  string     `json:"transition"`
	Source      string     `json:"source"`
	Payload     TaskIntent `json:"payload"`
}

func (f TaskStateMachine) Request(sc Scope, r TransitionRequest) (*Task, error) {
	if r.ActorUserID != sc.UserID || r.TaskID != sc.TaskID {
		return nil, ErrScope
	}
	r.Payload.Action = r.Transition
	r.Payload.Source = r.Source
	return f.Apply(sc, r.Payload)
}

type TransitionDenied struct {
	cause               error
	Allowed             bool     `json:"allowed"`
	ReasonCode          string   `json:"reason_code"`
	TaskID              string   `json:"task_id"`
	CurrentPhase        string   `json:"current_phase"`
	CurrentStep         string   `json:"current_step"`
	CurrentStatus       string   `json:"current_status"`
	RequestedTransition string   `json:"requested_transition"`
	AllowedTransitions  []string `json:"allowed_transitions"`
	Reason              string   `json:"reason"`
	StateChanged        bool     `json:"state_changed"`
}

func (d *TransitionDenied) Error() string {
	phase := map[string]string{"planning": "Подготовка задачи", "execution": "Выполнение", "validation": "Проверка", "done": "Завершено"}[d.CurrentPhase]
	return fmt.Sprintf("Переход отклонён. Этап: %s; шаг: %s.\n%s\nСначала выполните текущий шаг. Доступные действия: /task actions. Состояние не изменено.", phase, d.CurrentStep, d.Reason)
}
func (d *TransitionDenied) Unwrap() error {
	if d.cause != nil {
		return d.cause
	}
	return ErrTransition
}
func (f TaskStateMachine) deny(t *Task, action, code, reason string) error {
	keys := []string{}
	for _, d := range f.GetAllowedTransitions(t) {
		keys = append(keys, d.Key)
	}
	return &TransitionDenied{ReasonCode: code, TaskID: t.ID, CurrentPhase: t.Phase, CurrentStep: t.CurrentStep, CurrentStatus: t.Status, RequestedTransition: action, AllowedTransitions: keys, Reason: reason}
}
func (f TaskStateMachine) guard(tx *sql.Tx, sc Scope, t *Task, i TaskIntent, posting bool) error {
	d, ok := transitionFor(t, i.Action)
	if !ok {
		if i.Action == "confirm_task" && t.Phase == "planning" {
			return f.deny(t, i.Action, "PRECONDITION_FAILED", "Пока нельзя начать выполнение. Не завершена подготовка: выберите товар, задайте и подтвердите количество.")
		}
		return f.deny(t, i.Action, "INVALID_TRANSITION", "Перехода нет в таблице для текущего этапа и шага. Нельзя пропускать подготовку, выполнение или проверку.")
	}
	for _, p := range d.RequiredPermissions {
		if e := auth.Require(tx, sc.UserID, sc.WorkshopID, p); e != nil {
			return e
		}
	}
	action := "task_transition"
	if i.Action == "production_posted" {
		action = "complete_task"
	}
	if e := invariants.Check(tx, invariants.ProposedAction{ActionType: action, UserID: sc.UserID, WorkshopID: sc.WorkshopID, TaskID: t.ID}, invariants.Facts{Phase: t.Phase, ValidationPassed: t.State.ValidationResult == "passed"}); e != nil {
		return e
	}
	if i.Action == "confirm_task" && (t.State.ProductID <= 0 || t.State.Quantity <= 0) {
		return f.deny(t, i.Action, "PRECONDITION_FAILED", "Не хватает выбранного товара или положительного количества.")
	}
	if i.Action == "start_production" && !t.State.ParametersApproved {
		return f.deny(t, i.Action, "PRECONDITION_FAILED", "Параметры задачи ещё не утверждены.")
	}
	if i.Action == "verify_result" && t.State.ProducedQuantity == nil {
		return f.deny(t, i.Action, "PRECONDITION_FAILED", "Не указан фактический результат выполнения.")
	}
	if i.Action == "production_posted" && (t.State.ValidationResult != "passed" || !posting) {
		return f.deny(t, i.Action, "PRECONDITION_FAILED", "Нужны успешная проверка результата и подтверждённая складская проводка.")
	}
	if i.Action == "correct_result" && strings.TrimSpace(i.Reason) == "" {
		return f.deny(t, i.Action, "PRECONDITION_FAILED", "Укажите причину доработки: /task correct_result <причина>.")
	}
	if d.RequiresConfirmation && !i.Confirmed {
		return f.deny(t, i.Action, "CONFIRMATION_REQUIRED", "Проверьте параметры выбранной задачи и нажмите кнопку подтверждения. Фраза о запуске сама по себе не является подтверждением.")
	}
	return nil
}

func (f TaskStateMachine) saveTransition(tx *sql.Tx, sc Scope, before Task, t *Task, i TaskIntent) error {
	if strings.HasPrefix(i.Action, "legacy_") {
		if e := invariants.Check(tx, invariants.ProposedAction{ActionType: "task_transition", UserID: sc.UserID, WorkshopID: sc.WorkshopID, TaskID: t.ID}, invariants.Facts{Phase: t.Phase}); e != nil {
			return e
		}
	}
	d, ok := transitionFor(&before, i.Action)
	destination := func(rule, old, next string) bool {
		if rule == "*" {
			return old == next
		}
		return matches(rule, next)
	}
	previousStep := before.CurrentStep
	if before.FSMVersion == 0 && t.FSMVersion == 1 {
		previousStep = t.CurrentStep
	} // in-place legacy format adoption
	if !ok || !destination(d.ToPhase, before.Phase, t.Phase) || !destination(d.ToStep, previousStep, t.CurrentStep) || !destination(d.ToStatus, before.Status, t.Status) {
		return f.deny(&before, i.Action, "INVALID_DESTINATION", "Результат перехода не соответствует таблице.")
	}
	t.Version++
	t.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	_, e := tx.Exec(`UPDATE working_memory SET state_json=?,status=?,phase=?,current_step=?,expected_action=?,expected_action_type=?,started_at=?,paused_at=?,completed_at=?,updated_at=?,version=?,fsm_version=? WHERE task_id=? AND workshop_id=?`, compact(t.State), t.Status, t.Phase, t.CurrentStep, t.ExpectedAction, t.ExpectedActionType, t.StartedAt, t.PausedAt, t.CompletedAt, t.UpdatedAt, t.Version, t.FSMVersion, t.ID, sc.WorkshopID)
	if e != nil {
		return e
	}
	return taskEventDetails(tx, sc, before, *t, i)
}

// finishPosting is part of the same FSM, called only after stock posting in the
// caller's transaction. A failure rolls back both stock and the task transition.
func (f TaskStateMachine) finishPosting(tx *sql.Tx, sc Scope, t *Task, qty float64) error {
	i := TaskIntent{Action: "production_posted", Version: t.Version, Confirmed: true, Source: "confirmed_posting"}
	if e := f.guard(tx, sc, t, i, true); e != nil {
		return e
	}
	before := *t
	t.State.ProducedQuantity = &qty
	t.Status = "completed"
	setStep(t, "done", "completed", "none", "NONE")
	now := time.Now().UTC().Format(time.RFC3339Nano)
	t.CompletedAt = &now
	return f.saveTransition(tx, sc, before, t, i)
}
func (f TaskStateMachine) recordDenial(sc Scope, i TaskIntent, err error) {
	var d *TransitionDenied
	if !errors.As(err, &d) {
		return
	}
	_ = f.Memory.tx(sc, func(tx *sql.Tx) error {
		return memoryAudit(tx, sc, "TASK_TRANSITION_REJECTED", sc.TaskID, nil, d, i.Source)
	})
}
func (s *Service) TransitionTrace(sc Scope) (any, error) {
	t, e := s.Task(sc)
	if e != nil {
		return nil, e
	}
	var raw string
	e = s.DB.QueryRow(`SELECT metadata_json FROM audit_logs WHERE workshop_id=? AND actor_user_id=? AND event_type='TASK_TRANSITION_REJECTED' AND field_name=? ORDER BY id DESC LIMIT 1`, sc.WorkshopID, sc.UserID, sc.TaskID).Scan(&raw)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return nil, e
	}
	var rejected any
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &rejected)
	}
	return map[string]any{"current": t, "allowed_transitions": (TaskStateMachine{s}).AllowedTransitions(sc, t), "last_rejected_attempt": rejected}, nil
}
