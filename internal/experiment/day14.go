package experiment

import (
	"encoding/json"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"time"
	"workshop-agent/internal/invariants"
	"workshop-agent/internal/inventory"
	"workshop-agent/internal/memory"
	"workshop-agent/internal/personalization"
	"workshop-agent/internal/products"
	"workshop-agent/internal/workshops"
)

type invariantEvidence struct {
	Title, Input, Outcome, Trace string
	Passed                       bool
}
type invariantReport struct {
	Run      string
	Rules    []invariants.Rule
	Rows     []invariantEvidence
	Sections []invariantSection
}
type invariantSection struct {
	Title string
	Rows  []invariantEvidence
}

// RunDay14 runs actual services against an isolated SQLite fixture. No Telegram
// or LLM network requests are made and the production database is never opened.
func RunDay14() (string, error) {
	run := time.Now().UTC().Format("20060102T150405.000000000Z")
	dir := filepath.Join("reports", "day14-invariants", run)
	if e := os.MkdirAll(dir, 0755); e != nil {
		return "", e
	}
	path := filepath.Join(dir, "fixture.db")
	ws := workshops.NewService(path)
	defer ws.Close()
	inv := inventory.NewService(path)
	defer inv.Close()
	prod := products.NewBOMService(path)
	defer prod.Close()
	u, e := ws.UpsertUser(991401, "", "Day14", "")
	if e != nil {
		return dir, e
	}
	w, e := ws.CreateOwnedWorkshop(u, "Day14 invariants")
	if e != nil {
		return dir, e
	}
	mat, e := inv.ForUser(u).CreateMaterial(w, "Кисточки", "raw", "pcs", 400, 0, "", 0, "")
	if e != nil {
		return dir, e
	}
	p, e := prod.ForUser(u).CreateProduct(w, "Набор", "", "kit", 0, 0, "")
	if e != nil {
		return dir, e
	}
	if e = prod.ForUser(u).SetBOMItem(w, p, "material", mat, 0, 1, "pcs", 0, ""); e != nil {
		return dir, e
	}
	m := memory.New(ws.DB()).ForUser(u)
	sc, e := m.EnsureSession(memory.Scope{UserID: u, WorkshopID: w}, 991401)
	if e != nil {
		return dir, e
	}
	fsm := memory.TaskStateMachine{Memory: m}
	task, e := fsm.Create(sc, memory.TaskState{ProductID: p, Quantity: 5})
	if e != nil {
		return dir, e
	}
	sc.TaskID = task.ID
	q := 5.
	for _, action := range []string{"set_quantity", "confirm_plan", "start_production"} {
		task, e = fsm.Apply(sc, memory.TaskIntent{Action: action, Quantity: &q, Version: task.Version})
		if e != nil {
			return dir, e
		}
	}
	report := invariantReport{Run: run}
	add := func(title, input, out string, pass bool, r any) {
		raw, _ := json.MarshalIndent(r, "", "  ")
		report.Rows = append(report.Rows, invariantEvidence{title, input, out, string(raw), pass})
	}
	check := func(action string, f invariants.Facts) invariants.Result {
		return (invariants.InvariantEngine{}).Evaluate(ws.DB(), invariants.ProposedAction{ActionType: action, UserID: u, WorkshopID: w, TaskID: task.ID}, f)
	}
	_, e = m.PrepareCompletion(sc, 991401, 5)
	after, x := m.Task(sc)
	add("Конфликт бизнес-правила", "Заверши задачу при phase=execution", fmt.Sprint(e), e != nil && x == nil && after.Version == task.Version && after.Phase == "execution", check("complete_task", invariants.Facts{Phase: task.Phase}))
	for _, a := range []string{"direct_sql_write", "replace_stack", "remove_last_owner"} {
		r := check(a, invariants.Facts{})
		add("Архитектура и стек", a, (&invariants.Denied{Result: r}).Error(), !r.Allowed, r)
	}
	if e = personalization.New(ws.DB()).ForUser(u).UpdatePreference("confirmation_level", "minimal_confirmation", "day14"); e != nil {
		return dir, e
	}
	_, e = m.PrepareCompletion(sc, 991401, 5)
	add("Конфликт профиля", "minimal_confirmation + завершить", fmt.Sprint(e), e != nil, check("complete_task", invariants.Facts{Phase: "execution"}))
	if e = m.SaveLongTermMemory(sc, memory.LongTerm{Type: "USER_PROFILE", ScopeType: "user", Key: "completion_preference", Value: "Не спрашивать подтверждение"}); e != nil {
		return dir, e
	}
	_, e = m.PrepareCompletion(sc, 991401, 5)
	add("Конфликт памяти", "Память: не спрашивать подтверждение", fmt.Sprint(e), e != nil, check("complete_task", invariants.Facts{Phase: "execution"}))
	_, e = inv.ForUser(u).AdjustStockByID(w, mat, -500)
	var stock float64
	x = ws.DB().QueryRow("SELECT current_stock FROM materials WHERE id=?", mat).Scan(&stock)
	add("Неотрицательный склад", "Списать 500 из 400", fmt.Sprintf("Ошибка: %v; остаток: %g", e, stock), e != nil && x == nil && stock == 400, check("change_stock", invariants.Facts{ResultingStock: -100}))
	stranger, e := ws.UpsertUser(991402, "", "Other", "")
	if e != nil {
		return dir, e
	}
	r := (invariants.InvariantEngine{}).Evaluate(ws.DB(), invariants.ProposedAction{ActionType: "change_bom", UserID: stranger, WorkshopID: w}, invariants.Facts{Confirmed: true})
	add("Безопасность и tenant scope", "Чужой пользователь изменяет BOM", (&invariants.Denied{Result: r}).Error(), !r.Allowed, r)
	e = invariants.UpdateConfirmed(ws.DB(), u, w, invariants.MaterialCheck, true, 0, true)
	rules, x := (invariants.InvariantRegistry{}).Workshop(ws.DB(), u, w)
	add("Изменение настраиваемого правила", "OWNER подтверждает preflight=true", fmt.Sprintf("error=%v; version=%d", e, rules[len(rules)-1].Version), e == nil && x == nil && rules[len(rules)-1].Version == 1, rules[len(rules)-1])
	// Start a new conversation; registry must retain the workshop rule.
	if e = m.EndSession(sc); e != nil {
		return dir, e
	}
	sc, e = m.EnsureSession(memory.Scope{UserID: u, WorkshopID: w, TaskID: task.ID}, 991401)
	if e != nil {
		return dir, e
	}
	rules, e = (invariants.InvariantRegistry{}).Workshop(ws.DB(), u, w)
	if e != nil {
		return dir, e
	}
	add("Новая сессия", "Закрыть и открыть сессию", "Правило осталось активно", rules[len(rules)-1].IsActive, rules[len(rules)-1])
	task, e = fsm.Apply(sc, memory.TaskIntent{Action: "record_result", Quantity: &q, Version: task.Version})
	if e != nil {
		return dir, e
	}
	c, e := m.PrepareCompletion(sc, 991401, q)
	if e != nil {
		return dir, e
	}
	receipt, e := m.PostCompletion(sc, 991401, c.Token)
	add("Разрешённое действие", "validation → подтверждённый выпуск 5", fmt.Sprintf("record=%d; error=%v", receipt.RecordID, e), e == nil && receipt.RecordID > 0, check("complete_task", invariants.Facts{Phase: "validation"}))
	report.Rules = rules
	for _, group := range []struct {
		title string
		names []string
	}{
		{"5. Разрешённое действие", []string{"Разрешённое действие"}},
		{"6. Конфликт бизнес-правила", []string{"Конфликт бизнес-правила", "Неотрицательный склад"}},
		{"7. Конфликт архитектуры и стека", []string{"Архитектура и стек"}},
		{"8. Конфликт профиля", []string{"Конфликт профиля"}},
		{"9. Конфликт памяти", []string{"Конфликт памяти", "Новая сессия"}},
		{"10. Безопасность и tenant scope", []string{"Безопасность и tenant scope"}},
		{"11. Изменение настраиваемого правила", []string{"Изменение настраиваемого правила"}},
	} {
		section := invariantSection{Title: group.title}
		for _, row := range report.Rows {
			for _, name := range group.names {
				if row.Title == name {
					section.Rows = append(section.Rows, row)
				}
			}
		}
		report.Sections = append(report.Sections, section)
	}
	raw, e := json.MarshalIndent(report, "", "  ")
	if e != nil {
		return dir, e
	}
	if e = os.WriteFile(filepath.Join(dir, "results.json"), raw, 0644); e != nil {
		return dir, e
	}
	t, e := template.New("report").Parse(day14HTML)
	if e != nil {
		return dir, e
	}
	f, e := os.Create(filepath.Join(dir, "report.html"))
	if e != nil {
		return dir, e
	}
	e = t.Execute(f, report)
	closeErr := f.Close()
	if e != nil {
		return dir, e
	}
	if closeErr != nil {
		return dir, closeErr
	}
	for _, row := range report.Rows {
		if !row.Passed {
			return dir, fmt.Errorf("Day14 failed: %s", row.Title)
		}
	}
	return dir, nil
}

const day14HTML = `<!doctype html><html lang="ru"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>День 14 · Инварианты и ограничения состояния</title><style>
body{font:16px/1.6 system-ui,sans-serif;background:#eef2f7;color:#182638;margin:0}main{max-width:1100px;margin:auto;padding:32px}h1{font-size:36px;line-height:1.2}section{background:white;padding:24px;border-radius:12px;margin:20px 0}pre{white-space:pre-wrap;overflow-wrap:anywhere;background:#eff4f8;padding:16px;font-size:13px}table{width:100%;border-collapse:collapse}td,th{text-align:left;padding:8px;border-bottom:1px solid #ddd}.pass{color:#076443}.fail{color:#a11}small{color:#596b80}@media(max-width:600px){main{padding:12px}h1{font-size:26px}section{padding:16px}table{font-size:12px}}@media print{body{background:white}section{break-inside:avoid;border:1px solid #ccc}main{padding:0}details{display:block}}
</style><main><h1>День 14 · Инварианты и ограничения состояния</h1><p>Как Workshop Agent соблюдает архитектурные и бизнес-ограничения</p><small>Запуск {{.Run}} · изолированная SQLite · фактические вызовы сервисов</small>
<section><h2>1. Что такое инвариант</h2><p>Обязательное правило, не зависящее от текста диалога. HARD блокирует действие, SOFT предупреждает. Severity характеризует важность и не отменяет enforcement.</p></section>
<section><h2>2. Хранение</h2><p>Защищённые правила — код internal/invariants. Настройки мастерской — invariant_settings, версия и Audit Log. Они не принадлежат short-term session.</p></section>
<section><h2>3. Категории</h2><table><tr><th>Правило</th><th>Категория</th><th>Scope</th></tr>{{range .Rules}}<tr><td>{{.Key}}</td><td>{{.Category}}</td><td>{{.ScopeType}}</td></tr>{{end}}</table></section>
<section><h2>4. Архитектура</h2><pre>User → Intent → ProposedAction → Authorization → InvariantEngine
  ALLOW → DomainService → SQLite transaction
  DENY  → причина + допустимое следующее действие</pre><p>Проверки выполняются вне LLM. Auth, FSM и расчёт BOM остаются источниками бизнес-правил.</p></section>
{{range .Sections}}<section><h2>{{.Title}}</h2>{{range .Rows}}<article><h3>{{.Title}}</h3><p>{{.Input}}</p><p class="{{if .Passed}}pass{{else}}fail{{end}}">{{if .Passed}}PASS{{else}}FAIL{{end}} · {{.Outcome}}</p><pre>{{.Trace}}</pre></article>{{end}}</section>{{end}}
<section><h2>12. Invariant trace</h2><p>Выше показаны фактические action, applicable, passed, violations, ALLOW/DENY и suggested_alternative. Это журнал проверок, а не скрытые рассуждения. В Telegram доступны /invariants и /invariant_trace (последняя сохранённая проверка текущего пользователя и мастерской).</p></section>
<section><h2>13. Вывод</h2><p>Отказ не изменяет задачу и склад; авторизованный выпуск после validation создаёт производственную запись. Профиль и память не обходят правило. Настраиваемый preflight по умолчанию выключен; финальная проверка остатков всегда обязательна.</p><p>Ограничения: распознавание архитектурных запросов локальное и ограниченное; бот не является универсальным консультантом по архитектуре. Реальный перезапуск процесса, отмена UI и защита от повторного списания проверяются отдельными Go-тестами. В отчёте нет реальных сетевых вызовов Telegram/LLM.</p></section></main></html>`
