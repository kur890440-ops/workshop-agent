package experiment

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"time"
	"workshop-agent/internal/agent"
	"workshop-agent/internal/inventory"
	"workshop-agent/internal/llm"
	"workshop-agent/internal/memory"
	"workshop-agent/internal/products"
	"workshop-agent/internal/workshops"
)

type taskDemoRow struct {
	Input, Response, Error string
	State                  *memory.Task
}
type taskDemo struct {
	Run     string
	Rows    []taskDemoRow
	History any
	Checks  map[string]bool
}

func RunDay13() (string, error) {
	run := time.Now().UTC().Format("20060102T150405.000000000Z")
	dir := filepath.Join("reports", "day13-task-state", run)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "fixture.db")
	var ws *workshops.Service
	var inv *inventory.Service
	var prod *products.Service
	var a *agent.WorkshopAgent
	open := func() {
		ws = workshops.NewService(path)
		inv = inventory.NewService(path)
		prod = products.NewBOMService(path)
		a = agent.NewWorkshopAgent(&llm.MockClient{}, ws, inv, prod)
	}
	close := func() { ws.Close(); inv.Close(); prod.Close() }
	open()
	defer func() { close() }()
	u, err := ws.UpsertUser(991301, "", "Day13", "")
	if err != nil {
		return dir, err
	}
	w, err := ws.CreateOwnedWorkshop(u, "Учебная мастерская")
	if err != nil {
		return dir, err
	}
	productID, err := prod.ForUser(u).CreateProduct(w, "дефлекторов", "", "product", 0, 0, "")
	if err != nil {
		return dir, err
	}
	report := taskDemo{Run: run, Checks: map[string]bool{}}
	materialID, err := inv.ForUser(u).CreateMaterial(w, "Компонент", "raw", "pcs", 100, 0, "", 0, "")
	if err != nil {
		return dir, err
	}
	if err = prod.ForUser(u).SetBOMItem(w, productID, "material", materialID, 0, 1, "pcs", 0, ""); err != nil {
		return dir, err
	}
	var id string
	call := func(text string, wantError bool) error {
		answer, _, e := a.HandleMessageForWorkshop(context.Background(), w, u, 991301, text)
		row := taskDemoRow{Input: text, Response: answer}
		if e != nil {
			row.Error = e.Error()
		}
		if id == "" {
			active, x := a.Memory.ForUser(u).ActiveWorking(memory.Scope{UserID: u, WorkshopID: w})
			if x != nil {
				return x
			}
			if active != nil {
				id = active.ID
			}
		}
		if id != "" {
			row.State, _ = a.Memory.ForUser(u).Task(memory.Scope{UserID: u, WorkshopID: w, TaskID: id})
		}
		report.Rows = append(report.Rows, row)
		if (e != nil) != wantError {
			return fmt.Errorf("unexpected result for %q: %v", text, e)
		}
		return nil
	}
	for _, text := range []string{"Сделаем 20 дефлекторов.", "/task pause", "/task resume", "Нет, 25.", "/task confirm_task", "Поставь пока на паузу."} {
		if err = call(text, false); err != nil {
			return dir, err
		}
	}
	close()
	open()
	for _, text := range []string{"/session new", "Продолжим.", "что сейчас нужно от меня?", "/task start_production", "/task result 25", "/task pause", "/task resume"} {
		if err = call(text, false); err != nil {
			return dir, err
		}
	}
	if err = call("/task complete", true); err != nil {
		return dir, err
	}
	for _, text := range []string{"/task verify_result"} {
		if err = call(text, false); err != nil {
			return dir, err
		}
	}
	sc := memory.Scope{UserID: u, WorkshopID: w, TaskID: id}
	sc, err = a.Memory.ForUser(u).EnsureSession(sc, 991301)
	if err != nil {
		return dir, err
	}
	preview, err := a.Memory.ForUser(u).PrepareCompletion(sc, 991301, 25)
	if err != nil {
		return dir, err
	}
	if _, err = a.Memory.ForUser(u).PostCompletion(sc, 991301, preview.Token); err != nil {
		return dir, err
	}
	if err = call("/task resume "+id, true); err != nil {
		return dir, err
	}
	last, err := a.Memory.ForUser(u).Task(sc)
	if err != nil {
		return dir, err
	}
	report.History, err = a.Memory.ForUser(u).TaskHistory(sc)
	if err != nil {
		return dir, err
	}
	report.Checks["done_terminal"] = last.Phase == "done" && last.Status == "completed"
	report.Checks["quantity_preserved_after_reopen"] = report.Rows[7].State.State.Quantity == 25 && report.Rows[7].State.Phase == "execution"
	var count int
	err = ws.DB().QueryRow("SELECT (SELECT COUNT(*) FROM production_records)+(SELECT COUNT(*) FROM inventory_movements)+(SELECT COUNT(*) FROM product_movements)").Scan(&count)
	if err != nil {
		return dir, err
	}
	report.Checks["production_posted_once"] = count == 3
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return dir, err
	}
	if err = os.WriteFile(filepath.Join(dir, "results.json"), raw, 0644); err != nil {
		return dir, err
	}
	t, err := template.New("day13").Parse(day13HTML)
	if err != nil {
		return dir, err
	}
	f, err := os.Create(filepath.Join(dir, "report.html"))
	if err != nil {
		return dir, err
	}
	defer f.Close()
	err = t.Execute(f, report)
	return filepath.Join(dir, "report.html"), err
}

const day13HTML = `<!doctype html><html lang="ru"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>День 13 · Состояние задачи</title><style>*{box-sizing:border-box}body{margin:0;background:#edf3f5;color:#18363d;font:16px/1.6 system-ui}main{max-width:1100px;margin:auto;padding:28px}section,header,article{background:white;padding:24px;border-radius:16px;margin:18px 0}h1{font-size:clamp(28px,5vw,46px);line-height:1.15}h2{color:#176b68}pre{white-space:pre-wrap;overflow-wrap:anywhere;background:#f0f5f6;padding:16px}p{overflow-wrap:anywhere}.grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(min(100%,300px),1fr));gap:16px}.state{border-left:4px solid #168580;padding:12px;background:#f2f8f7}@media(max-width:600px){main{padding:12px}section,article,header{padding:16px}}@media print{body{background:white;font-size:11pt}main{padding:0}section,article{border:1px solid #ddd;border-radius:0}h2{break-after:avoid}}</style><main>
<header><p>WORKSHOP AGENT · {{.Run}}</p><h1>День 13 · Состояние задачи</h1><p>Task State Machine в Workshop Agent</p></header>
<section><h2>1. Зачем Task State Machine</h2><p>Продолжение без повторного объяснения: параметры и следующий шаг восстанавливаются из SQLite, а не из старого разговора.</p><h2>2. Модель Task State</h2><p>Та же working_memory: task_id, user_id, workshop_id, task_type, state_json; phase, current_step, expected_action/type, status, version, fsm_version, created/updated/started/paused/completed_at. История — task_transitions, атомарно с состоянием и Audit Log.</p><h2>3. State diagram</h2><pre>PLANNING
    ↓
EXECUTION
    ↓
VALIDATION
    ↓
DONE

Исправление результата:
VALIDATION → EXECUTION
ACTIVE ↔ PAUSED: любой незавершённый этап
CANCELLED / FAILED: терминальные lifecycle statuses</pre><h2>4. Phase / Step / Expected Action</h2><p>planning: select_product → set_quantity → confirm_task; execution: start_production → record_result; validation: verify_result → confirm_completion; done: completed. Шаги и ожидаемые действия задаёт код; LLM может передать только intent.</p><h2>5. Working Memory vs Task State</h2><p>Working Memory: какие данные собраны? FSM: какой этап, шаг и что ожидается? Short-Term: последние реплики. Domain DB: факты склада/производства. Заявленный результат задачи не создаёт production_records или списание.</p></section>
<section><h2>6. Обычный проход задачи</h2><p>Ниже реальные вызовы обработчика и снимки SQLite на отдельной fixture.db. Нет придуманных ответов или LLM-вызовов. Перед «Продолжим» соединения сервисов закрываются и открываются заново, затем начинается новая сессия.</p></section>
{{range .Rows}}<article><p><b>USER:</b> {{.Input}}</p>{{if .Response}}<pre>{{.Response}}</pre>{{end}}{{if .Error}}<p><b>Отклонено:</b> {{.Error}}</p>{{end}}{{if .State}}<div class="state">{{.State.Phase}} / {{.State.CurrentStep}} · {{.State.Status}}<br>Expected: {{.State.ExpectedAction}}<br>Товар: {{.State.State.ProductName}} · Задача: {{.State.State.Quantity}} · Версия: {{.State.Version}}</div>{{end}}</article>{{end}}
<section><h2>7–9. Pause в planning, execution, validation</h2><p>Сценарий выше делает паузу на каждом этапе: меняется только lifecycle status и метаданные перехода. Шаг, ожидаемое действие и task_data сохраняются.</p><h2>10. Resume в новой session</h2><p>«Продолжим» после переоткрытия БД восстанавливает 25 дефлекторов и execution/start_production. Данные старого диалога не нужны. Отдельный Go integration test также запускает дочерний OS-процесс, который загружает и продолжает задачу.</p><h2>11. Invalid transitions</h2><p>Complete до confirm_completion, переход из DONE, повторный callback с устаревшей версией и изменения чужой задачи запрещены. Две активные задачи недопустимы; несколько paused требуют явного выбора ID.</p><h2>12. Transition history</h2>{{range .History}}<p>{{index . "action"}}: {{index . "from"}} → {{index . "to"}} · {{index . "status"}} · {{index . "at"}}</p>{{end}}<h2>13. Вывод</h2>{{range $k,$v:=.Checks}}<p>{{$k}}: <b>{{$v}}</b></p>{{end}}<p>Task — единая сущность работы; production и assembly — типы задачи. Legacy-задачи сохранены с fsm_version=0, без автоматического объявления их выполненными по новым правилам. Завершение выполняется через подтверждённую атомарную проводку выпуска и компонентов. Все новые переходы проходят авторизацию и проверку версии. Python/pytest suite нет: используются Go tests. Day 15 не выполняется.</p></section><footer>Автономный HTML без CDN · results.json · fixture.db · рабочая БД не использовалась.</footer></main></html>`
