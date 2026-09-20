package experiment

import (
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"time"
	"workshop-agent/internal/auth"
	"workshop-agent/internal/inventory"
	"workshop-agent/internal/memory"
	"workshop-agent/internal/products"
	"workshop-agent/internal/workshops"
)

// RunDay15 exercises the production FSM against an isolated SQLite fixture.
func RunDay15() (string, error) {
	dir := filepath.Join("reports", "day15-controlled-transitions", time.Now().UTC().Format("20060102T150405.000000000Z"))
	if e := os.MkdirAll(dir, 0755); e != nil {
		return dir, e
	}
	path := filepath.Join(dir, "fixture.db")
	ws := workshops.NewService(path)
	defer func() { ws.Close() }()
	inv := inventory.NewService(path)
	defer func() { inv.Close() }()
	prod := products.NewBOMService(path)
	defer func() { prod.Close() }()
	u, e := ws.UpsertUser(991501, "", "Day15", "")
	if e != nil {
		return dir, e
	}
	w, e := ws.CreateOwnedWorkshop(u, "Day15 fixture")
	if e != nil {
		return dir, e
	}
	p, e := prod.ForUser(u).CreateProduct(w, "Дефлектор", "", "product", 0, 0, "")
	if e != nil {
		return dir, e
	}
	material, e := inv.ForUser(u).CreateMaterial(w, "Коробка", "packaging", "pcs", 100, 0, "", 0, "")
	if e != nil {
		return dir, e
	}
	if e = prod.ForUser(u).SetBOMItem(w, p, "material", material, 0, 1, "pcs", 0, ""); e != nil {
		return dir, e
	}
	m := memory.New(ws.DB()).ForUser(u)
	sc, e := m.EnsureSession(memory.Scope{UserID: u, WorkshopID: w}, 991501)
	if e != nil {
		return dir, e
	}
	f := memory.TaskStateMachine{Memory: m}
	task, e := f.Create(sc, memory.TaskState{ProductID: p, Quantity: 2})
	if e != nil {
		return dir, e
	}
	sc.TaskID = task.ID
	type row struct {
		Scenario, Action, Decision, Reason string
		State                              *memory.Task
	}
	report := struct {
		Checks      map[string]bool
		Rows        []row
		Definitions any
		History     any
		Trace       any
	}{Checks: map[string]bool{}, Definitions: memory.TransitionDefinitions()}
	run := func(key, action string, confirmed bool, q *float64, wantAllow bool) error {
		before := task.Version
		updated, err := f.Request(sc, memory.TransitionRequest{TaskID: sc.TaskID, ActorUserID: u, Transition: action, Source: "day15_report", Payload: memory.TaskIntent{Version: task.Version, Confirmed: confirmed, Quantity: q, Reason: "Необходима доработка"}})
		r := row{Scenario: key, Action: action, Decision: "ALLOW"}
		if err != nil {
			r.Decision = "DENY"
			r.Reason = err.Error()
			updated, e = m.Task(sc)
			if e != nil {
				return e
			}
		}
		task = updated
		r.State = task
		report.Rows = append(report.Rows, r)
		report.Checks[key] = (err == nil) == wantAllow && (wantAllow || task.Version == before)
		if !report.Checks[key] {
			return fmt.Errorf("Day15 %s unexpected result: %v", key, err)
		}
		return nil
	}
	pauseResume := func(key string) error {
		phase, step, expected := task.Phase, task.CurrentStep, task.ExpectedAction
		state, _ := json.Marshal(task.State)
		if e := run(key+" pause", "pause", false, nil, true); e != nil {
			return e
		}
		if e := run(key+" resume", "resume", false, nil, true); e != nil {
			return e
		}
		after, _ := json.Marshal(task.State)
		report.Checks[key+" state preserved"] = task.Phase == phase && task.CurrentStep == step && task.ExpectedAction == expected && string(state) == string(after)
		return nil
	}
	if e = run("A planning cannot complete", "complete", true, nil, false); e != nil {
		return dir, e
	}
	if e = pauseResume("I planning"); e != nil {
		return dir, e
	}
	q := 2.
	if e = run("quantity", "set_quantity", false, &q, true); e != nil {
		return dir, e
	}
	stale := task.Version
	if e = run("edit quantity", "set_quantity", false, &q, true); e != nil {
		return dir, e
	}
	_, e = f.Apply(sc, memory.TaskIntent{Action: "confirm_task", Version: stale, Confirmed: true})
	report.Checks["L stale confirmation"] = errors.Is(e, memory.ErrTaskChanged)
	if e = run("B confirmation required", "confirm_task", false, nil, false); e != nil {
		return dir, e
	}
	stale = task.Version
	if e = run("C approval", "confirm_task", true, nil, true); e != nil {
		return dir, e
	}
	_, e = f.Apply(sc, memory.TaskIntent{Action: "confirm_task", Version: stale, Confirmed: true})
	report.Checks["M duplicate confirmation"] = errors.Is(e, memory.ErrTaskChanged)
	if e = run("D execution cannot complete", "complete", true, nil, false); e != nil {
		return dir, e
	}
	if e = run("start execution", "start_production", true, nil, true); e != nil {
		return dir, e
	}
	if e = pauseResume("J execution"); e != nil {
		return dir, e
	}
	if e = run("P pause before restart", "pause", false, nil, true); e != nil {
		return dir, e
	}
	saved, _ := json.Marshal(task.State)
	step := task.CurrentStep
	ws.Close()
	inv.Close()
	prod.Close()
	ws = workshops.NewService(path)
	inv = inventory.NewService(path)
	prod = products.NewBOMService(path)
	m = memory.New(ws.DB()).ForUser(u)
	f = memory.TaskStateMachine{Memory: m}
	sc, e = m.EnsureSession(sc, 991502)
	if e != nil {
		return dir, e
	}
	if e = run("P resume after restart", "resume", false, nil, true); e != nil {
		return dir, e
	}
	after, _ := json.Marshal(task.State)
	report.Checks["P state persisted"] = task.Phase == "execution" && task.CurrentStep == step && string(saved) == string(after)
	if e = run("missing execution result", "record_result", false, nil, false); e != nil {
		return dir, e
	}
	if e = run("E execution completed", "record_result", false, &q, true); e != nil {
		return dir, e
	}
	if e = run("F missing validation result", "production_posted", true, nil, false); e != nil {
		return dir, e
	}
	if e = pauseResume("K validation"); e != nil {
		return dir, e
	}
	if e = run("N user insists", "complete", true, nil, false); e != nil {
		return dir, e
	}
	if e = run("N owner insists again", "complete", true, nil, false); e != nil {
		return dir, e
	}
	if e = run("validation failed", "correct_result", false, nil, true); e != nil {
		return dir, e
	}
	report.Checks["return reason preserved"] = task.State.ValidationReason != "" && task.State.ValidationResult == "failed"
	if e = run("repeat execution", "record_result", false, &q, true); e != nil {
		return dir, e
	}
	if e = run("validation passed", "verify_result", true, nil, true); e != nil {
		return dir, e
	}
	if _, e = ws.DB().Exec("UPDATE workshop_members SET role='VIEWER' WHERE user_id=? AND workshop_id=?", u, w); e != nil {
		return dir, e
	}
	_, e = f.Apply(sc, memory.TaskIntent{Action: "pause", Version: task.Version})
	report.Checks["O authorization"] = errors.Is(e, auth.ErrDenied)
	if _, e = ws.DB().Exec("UPDATE workshop_members SET role='OWNER' WHERE user_id=? AND workshop_id=?", u, w); e != nil {
		return dir, e
	}
	preview, e := m.PrepareCompletion(sc, 991502, q)
	if e != nil {
		return dir, e
	}
	receipt, e := m.PostCompletion(sc, 991502, preview.Token)
	if e != nil {
		return dir, e
	}
	task, e = m.Task(sc)
	if e != nil {
		return dir, e
	}
	report.Checks["G done after validated posting"] = task.Phase == "done" && task.Status == "completed" && task.State.ValidationResult == "passed" && receipt.RecordID > 0
	again, e := m.PostCompletion(sc, 991502, preview.Token)
	report.Checks["M posting once"] = e == nil && again.RecordID == receipt.RecordID
	if e = run("H done terminal", "resume", false, nil, false); e != nil {
		return dir, e
	}
	report.History, e = m.TaskHistory(sc)
	if e != nil {
		return dir, e
	}
	report.Trace, e = m.TransitionTrace(sc)
	if e != nil {
		return dir, e
	}
	for key, passed := range report.Checks {
		if !passed {
			return dir, fmt.Errorf("Day15 failed: %s", key)
		}
	}
	raw, e := json.MarshalIndent(report, "", "  ")
	if e != nil {
		return dir, e
	}
	if e = os.WriteFile(filepath.Join(dir, "results.json"), raw, 0644); e != nil {
		return dir, e
	}
	page, e := template.New("day15").Parse(`<!doctype html><html lang="ru"><meta charset="utf-8"><title>Day 15 — Контролируемые переходы</title><style>body{max-width:1100px;margin:40px auto;font:16px system-ui;background:#f6f8fc;color:#172033}table{border-collapse:collapse;width:100%;background:white}td,th{padding:10px;border:1px solid #ddd;text-align:left}pre{white-space:pre-wrap}small{color:#556}</style><h1>Day 15 — Контролируемые переходы</h1><p>Один TaskStateMachine. Данные — отдельная SQLite fixture; Telegram и LLM не использовались.</p><h2>Проверки</h2>{{range $key,$value:=.Checks}}<p>{{$key}}: <b>{{$value}}</b></p>{{end}}<h2>Фактические переходы и отказы</h2><table><tr><th>Сценарий</th><th>Запрос</th><th>Решение</th><th>Состояние после запроса</th></tr>{{range .Rows}}<tr><td>{{.Scenario}}</td><td>{{.Action}}</td><td>{{.Decision}}<br><small>{{.Reason}}</small></td><td>{{.State.Phase}} / {{.State.CurrentStep}} / {{.State.Status}}</td></tr>{{end}}</table><p>Полные определения, история и trace: <a href="results.json">results.json</a>.</p></html>`)
	if e != nil {
		return dir, e
	}
	file, e := os.Create(filepath.Join(dir, "report.html"))
	if e != nil {
		return dir, e
	}
	defer file.Close()
	e = page.Execute(file, report)
	return dir, e
}
