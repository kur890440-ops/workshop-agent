// Package invariants defines policy independently of conversational memory.
// Facts are supplied by domain services, never accepted from an LLM response.
package invariants

import (
	"fmt"
	"math"
	"strings"
	"workshop-agent/internal/auth"
)

type Rule struct {
	ID          string   `json:"id"`
	Key         string   `json:"key"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Category    string   `json:"category"`
	ScopeType   string   `json:"scope_type"`
	ScopeID     *int64   `json:"scope_id"`
	Severity    string   `json:"severity"`
	Enforcement string   `json:"enforcement"`
	IsActive    bool     `json:"is_active"`
	Version     int      `json:"version"`
	CreatedAt   string   `json:"created_at"`
	UpdatedAt   string   `json:"updated_at"`
	Modifiable  bool     `json:"modifiable"`
	Actions     []string `json:"actions"`
}
type ProposedAction struct {
	ActionType string         `json:"action_type"`
	UserID     int64          `json:"user_id"`
	WorkshopID int64          `json:"workshop_id"`
	TaskID     string         `json:"task_id,omitempty"`
	Payload    map[string]any `json:"payload,omitempty"`
	Scope      ScopeContext   `json:"scope"`
}
type Facts struct {
	Phase            string
	ResultingStock   float64
	Confirmed        bool
	MaterialShortage string
}
type Violation struct {
	InvariantKey string `json:"invariant_key"`
	Title        string `json:"title"`
	Reason       string `json:"reason"`
	Severity     string `json:"severity"`
}
type Result struct {
	Allowed              bool           `json:"allowed"`
	Decision             string         `json:"decision"`
	Action               ProposedAction `json:"action"`
	Applicable           []string       `json:"applicable"`
	Passed               []string       `json:"passed"`
	Violations           []Violation    `json:"violations"`
	Warnings             []Violation    `json:"warnings"`
	SuggestedAlternative string         `json:"suggested_alternative"`
}
type Denied struct{ Result Result }

func (e *Denied) Error() string {
	parts := []string{"Действие «" + e.Result.Action.ActionType + "» отклонено."}
	for _, v := range e.Result.Violations {
		parts = append(parts, v.Title+" ["+v.InvariantKey+"]: "+v.Reason)
	}
	return strings.Join(parts, "\n") + "\n" + e.Result.SuggestedAlternative
}

type InvariantRegistry struct{}

func (InvariantRegistry) System() []Rule {
	specs := []struct {
		key, title, cat string
		actions         []string
	}{
		{"authorized_workshop", "Доступ только через Membership и Permissions", "SECURITY_RULE", nil},
		{"domain_services_only", "Изменения только через доменные сервисы", "ARCHITECTURE", []string{"direct_sql_write"}},
		{"fsm_only", "Состояние задачи изменяется только через FSM", "TECHNICAL_DECISION", []string{"direct_task_state"}},
		{"go_sqlite_telegram", "Текущий стек: Go, SQLite, Telegram", "STACK_CONSTRAINT", []string{"replace_stack"}},
		{"validation_before_completion", "Перед завершением требуется validation", "BUSINESS_RULE", []string{"complete_task"}},
		{"nonnegative_inventory", "Остаток не может быть отрицательным", "DATA_INTEGRITY_RULE", []string{"change_stock"}},
		{"stock_depleted", "Нулевой остаток: проверьте потребность в закупке", "BUSINESS_RULE", []string{"change_stock"}},
		{"bom_confirmation", "Изменение состава требует подтверждения", "BUSINESS_RULE", []string{"change_bom"}},
		{"protected_ownership", "Последний OWNER защищён IdentityService", "SECURITY_RULE", []string{"remove_last_owner"}},
	}
	out := make([]Rule, 0, len(specs))
	for _, s := range specs {
		out = append(out, Rule{ID: s.key, Key: s.key, Title: s.title, Description: s.title, Category: s.cat, ScopeType: "SYSTEM", Severity: "CRITICAL", Enforcement: "HARD", IsActive: true, Version: 1, CreatedAt: "2026-09-17", UpdatedAt: "2026-09-17", Actions: s.actions})
		if s.key == "stock_depleted" {
			out[len(out)-1].Enforcement = "SOFT"
			out[len(out)-1].Severity = "WARNING"
		}
	}
	return out
}
func (r InvariantRegistry) ForAction(a ProposedAction) []Rule {
	var out []Rule
	for _, rule := range r.System() {
		if len(rule.Actions) == 0 {
			out = append(out, rule)
			continue
		}
		for _, action := range rule.Actions {
			if action == a.ActionType {
				out = append(out, rule)
				break
			}
		}
	}
	return out
}

type InvariantEngine struct{ Registry InvariantRegistry }

func (e InvariantEngine) Evaluate(q auth.Querier, a ProposedAction, f Facts) Result {
	r := Result{Allowed: true, Decision: "ALLOW", Action: a, SuggestedAlternative: "Используйте разрешённое действие в меню мастерской."}
	rules := e.Registry.ForAction(a)
	if a.ActionType == "start_production" {
		if scoped, err := e.Registry.Workshop(q, a.UserID, a.WorkshopID); err == nil {
			rules = append(rules, scoped[len(scoped)-1])
		}
	}
	for _, rule := range rules {
		scope := a.Scope
		scope.WorkshopID = a.WorkshopID
		if !Applies(rule, scope) {
			continue
		}
		if !rule.IsActive {
			continue
		}
		r.Applicable = append(r.Applicable, rule.Key)
		reason, alt := "", ""
		switch rule.Key {
		case MaterialCheck:
			if f.MaterialShortage != "" {
				reason = f.MaterialShortage
				alt = "Пополните материалы или измените план до запуска производства."
			}
		case "authorized_workshop":
			p := auth.WorkshopRead
			switch a.ActionType {
			case "complete_task":
				p = auth.ProductionCreate
			case "change_stock":
				p = auth.InventoryWrite
			case "change_bom":
				p = auth.BOMWrite
			}
			if err := auth.Require(q, a.UserID, a.WorkshopID, p); err != nil {
				reason = "Нет действующего доступа к этой мастерской или разрешения на действие."
				alt = "Обратитесь к OWNER или ADMIN за доступом."
			}
		case "validation_before_completion":
			if f.Phase != "validation" {
				reason = fmt.Sprintf("Текущая фаза: %s. Пропуск проверки запрещён.", f.Phase)
				alt = "Сначала укажите фактический выпуск через /task result <количество>, затем проверьте расчёт и подтвердите завершение."
			}
		case "nonnegative_inventory":
			if f.ResultingStock < 0 || math.IsNaN(f.ResultingStock) || math.IsInf(f.ResultingStock, 0) {
				reason = "Итоговый остаток недопустим."
				alt = "Уменьшите списание или зарегистрируйте фактическое поступление."
			}
		case "stock_depleted":
			if f.ResultingStock == 0 {
				reason = "Остаток станет нулевым."
				alt = "При необходимости запланируйте пополнение."
			}
		case "bom_confirmation":
			if !f.Confirmed {
				reason = "Изменение состава не подтверждено."
				alt = "Откройте состав товара, проверьте изменения и подтвердите сохранение."
			}
		case "domain_services_only":
			reason = "Прямая запись из LLM в SQLite обходит бизнес-проверки."
			alt = "Передайте структурированную команду в InventoryService после авторизации."
		case "fsm_only":
			reason = "Прямое изменение состояния обходит FSM."
			alt = "Используйте допустимый переход TaskStateMachine."
		case "go_sqlite_telegram":
			reason = "Замена рабочего persistence нарушает активное ограничение стека."
			alt = "Можно подготовить план миграции и интерфейс хранилища, сохранив текущий backend SQLite."
		case "protected_ownership":
			reason = "Нельзя удалить последнего владельца."
			alt = "Сначала выполните явную передачу ownership."
		}
		if reason != "" {
			v := Violation{rule.Key, rule.Title, reason, rule.Severity}
			if rule.Enforcement == "SOFT" {
				r.Warnings = append(r.Warnings, v)
			} else {
				r.Violations = append(r.Violations, v)
				r.Allowed = false
			}
			r.SuggestedAlternative = alt
		} else {
			r.Passed = append(r.Passed, rule.Key)
		}
		// Never disclose domain state following an authorization failure.
		if rule.Key == "authorized_workshop" && !r.Allowed {
			break
		}
	}
	if !r.Allowed {
		r.Decision = "DENY"
	} else if len(r.Warnings) > 0 {
		r.Decision = "WARN"
	}
	return r
}
func Check(q auth.Querier, a ProposedAction, f Facts) error {
	r := (InvariantEngine{}).Evaluate(q, a, f)
	if !r.Allowed {
		return &Denied{r}
	}
	return nil
}
