package invariants

import "strings"

// Policy advice is routed separately from domain commands. Unknown advice is
// never executable; the existing strict LLM command allowlist remains authoritative.
func AdviceAction(text string) string {
	s := strings.ToLower(strings.TrimSpace(text))
	if (strings.Contains(s, "sqlite") || strings.Contains(s, "склад")) && (strings.Contains(s, "минуя") || strings.Contains(s, "напрямую") || strings.Contains(s, "прямо из llm")) {
		return "direct_sql_write"
	}
	if strings.Contains(s, "postgres") && (strings.Contains(s, "перепиш") || strings.Contains(s, "замен") || strings.Contains(s, "перевед")) {
		return "replace_stack"
	}
	if strings.Contains(s, "fsm") && (strings.Contains(s, "обой") || strings.Contains(s, "минуя")) {
		return "direct_task_state"
	}
	return ""
}
