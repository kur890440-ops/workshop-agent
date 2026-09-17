package experiment

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"strings"
	"time"
	"workshop-agent/internal/agent"
	"workshop-agent/internal/inventory"
	"workshop-agent/internal/llm"
	"workshop-agent/internal/memory"
	"workshop-agent/internal/personalization"
	"workshop-agent/internal/products"
	"workshop-agent/internal/workshops"
)

type PersonalizationCase struct {
	Name, Input, Response, Error string
	Context                      memory.Context
	Usage                        *llm.Usage
	StocksPresent                bool
}
type PersonalizationReport struct {
	RunID, Model, CreatedAt string
	Temperature             int
	Cases                   []PersonalizationCase
	Checks                  map[string]bool
	Tokens                  int
}

func RunDay12(ctx context.Context, client *llm.OpenRouterClient) (string, error) {
	run := time.Now().UTC().Format("20060102T150405Z")
	dir := filepath.Join("reports", "day12-personalization", run)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "fixture.db")
	ws := workshops.NewService(path)
	defer ws.Close()
	inv := inventory.NewService(path)
	defer inv.Close()
	prod := products.NewBOMService(path)
	defer prod.Close()
	u, err := ws.UpsertUser(991201, "", "Учебный A", "")
	if err != nil {
		return dir, err
	}
	w, err := ws.CreateOwnedWorkshop(u, "Day12 учебная мастерская")
	if err != nil {
		return dir, err
	}
	other, err := ws.UpsertUser(991202, "", "Учебный B", "")
	if err != nil {
		return dir, err
	}
	_, token, err := ws.CreateInvite(u, w, "ADMIN", 0, 0)
	if err != nil {
		return dir, err
	}
	if _, err = ws.AcceptInvite(other, token); err != nil {
		return dir, err
	}
	for _, v := range []struct {
		name, unit string
		stock, min float64
	}{{"Гипс", "kg", 10, 2}, {"Кисточки", "pcs", 400, 50}, {"Коробки", "pcs", 100, 20}} {
		if _, err = inv.ForUser(u).CreateMaterial(w, v.name, "raw", v.unit, v.stock, v.min, "", 0, ""); err != nil {
			return dir, err
		}
	}
	a := agent.NewWorkshopAgent(client, ws, inv, prod)
	svc := personalization.New(ws.DB()).ForUser(u)
	set := func(key, value string) error { return svc.UpdatePreference(key, value, "day12_experiment") }
	for k, v := range map[string]string{"style": "neutral", "response_style": "concise", "response_format": "list", "summary_first": "true"} {
		if err = set(k, v); err != nil {
			return dir, err
		}
	}
	report := PersonalizationReport{RunID: run, Model: client.Model, CreatedAt: time.Now().Format(time.RFC3339), Checks: map[string]bool{}}
	save := func() error {
		raw, _ := json.MarshalIndent(report, "", "  ")
		if err := os.WriteFile(filepath.Join(dir, "results.json"), raw, 0644); err != nil {
			return err
		}
		t, err := template.New("report").Parse(day12HTML)
		if err != nil {
			return err
		}
		f, err := os.Create(filepath.Join(dir, "report.html"))
		if err != nil {
			return err
		}
		defer f.Close()
		return t.Execute(f, report)
	}
	question := "Покажи материалы мастерской и оцени текущее состояние."
	call := func(name, text string) error {
		built, err := a.BuildReportContext(u, w, 991201, text)
		if err != nil {
			return err
		}
		answer, usage, e := client.Complete(ctx, built.Prompt)
		row := PersonalizationCase{Name: name, Input: text, Response: answer, Context: built, Usage: usage}
		if e != nil {
			row.Error = "LLM request failed"
		}
		row.StocksPresent = strings.Contains(answer, "Гипс") && strings.Contains(answer, "10") && strings.Contains(answer, "400") && strings.Contains(answer, "100")
		if usage != nil {
			report.Tokens += usage.TotalTokens
		}
		report.Cases = append(report.Cases, row)
		fmt.Printf("Day12: %s; response=%t\n", name, e == nil)
		if err = save(); err != nil {
			return err
		}
		return e
	}
	if err = call("A · краткий список", question); err != nil {
		return dir, err
	}
	// Controlled A/B uses the same user/session and snapshot; only profile changes.
	for k, v := range map[string]string{"style": "technical", "response_style": "detailed", "response_format": "table", "summary_first": "false"} {
		if err = set(k, v); err != nil {
			return dir, err
		}
	}
	if err = call("B · подробная таблица", question); err != nil {
		return dir, err
	}
	report.Checks["controlled_context_same_except_profile"] = sameDay12Context(report.Cases[0].Context, report.Cases[1].Context)
	op, _ := personalization.New(ws.DB()).ForUser(other).GetProfile()
	report.Checks["user_isolation"] = op.Detail == "normal"
	for k, v := range map[string]string{"style": "neutral", "response_style": "concise", "response_format": "list", "summary_first": "true"} {
		if err = set(k, v); err != nil {
			return dir, err
		}
	}
	if _, _, err = a.HandleMessageForWorkshop(ctx, w, u, 991201, "/session new"); err != nil {
		return dir, err
	}
	if err = call("Автоматически в новой сессии", question); err != nil {
		return dir, err
	}
	if err = call("Временное переопределение", question+" В этот раз объясни подробно."); err != nil {
		return dir, err
	}
	p, _ := svc.GetProfile()
	report.Checks["temporary_did_not_persist"] = p.Detail == "brief"
	if err = call("Следующий запрос снова brief", question); err != nil {
		return dir, err
	}
	if _, _, err = a.HandleMessageForWorkshop(ctx, w, u, 991201, "Теперь всегда отвечай подробно."); err != nil {
		return dir, err
	}
	if _, _, err = a.HandleMessageForWorkshop(ctx, w, u, 991201, "/session new"); err != nil {
		return dir, err
	}
	if err = call("Постоянное изменение после новой сессии", question); err != nil {
		return dir, err
	}
	if err = call("Производственный отчёт: итог сначала", "Подготовь производственный отчет и покажи материалы."); err != nil {
		return dir, err
	}
	otherW, err := ws.CreateOwnedWorkshop(u, "Day12 вторая мастерская")
	if err != nil {
		return dir, err
	}
	p, _ = svc.GetProfile()
	report.Checks["workshop_switch_keeps_profile"] = otherW != w && p.Detail == "detailed"
	var moves int
	err = ws.DB().QueryRow("SELECT (SELECT COUNT(*) FROM inventory_movements)+(SELECT COUNT(*) FROM product_movements)+(SELECT COUNT(*) FROM production_records)").Scan(&moves)
	if err != nil {
		return dir, err
	}
	report.Checks["no_domain_mutations"] = moves == 0
	if err = save(); err != nil {
		return dir, err
	}
	return filepath.Join(dir, "report.html"), nil
}
func sameDay12Context(a, b memory.Context) bool {
	filter := func(c memory.Context) string {
		v := []memory.Item{}
		for _, i := range c.Trace.Items {
			if i.Layer != "USER_PROFILE" {
				v = append(v, i)
			}
		}
		raw, _ := json.Marshal(v)
		return string(raw)
	}
	return filter(a) == filter(b)
}

const day12HTML = `<!doctype html><html lang="ru"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>День 12 · Персонализация ассистента</title><style>*{box-sizing:border-box}body{margin:0;background:#eef2f5;color:#152c3a;font:16px/1.6 system-ui,sans-serif}main{max-width:1100px;margin:auto;padding:32px}header,section,article{background:white;border-radius:16px;padding:24px;margin-bottom:20px}h1{font-size:clamp(28px,5vw,48px);line-height:1.15}h2{color:#146761}pre{white-space:pre-wrap;overflow-wrap:anywhere;background:#f3f6f8;padding:16px;font:14px/1.5 ui-monospace,monospace}.grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(min(100%,330px),1fr));gap:18px}.tag{color:#146761;font-weight:700}.answer{white-space:pre-wrap;overflow-wrap:anywhere;border-left:4px solid #26978c;padding:16px;background:#f5faf9}details{margin-top:16px}footer{color:#526878}@media(max-width:600px){main{padding:12px}header,section,article{padding:16px}}@media print{body{background:white;font-size:11pt}main{max-width:none;padding:0}header,section,article{border:1px solid #ddd;border-radius:0}details{display:none}.answer{break-inside:avoid}h2,h3{break-after:avoid}}</style><main>
<header><p class="tag">WORKSHOP AGENT · DAY 12</p><h1>День 12 · Персонализация ассистента</h1><p>Как Workshop Agent адаптирует ответы под конкретного пользователя</p><p>{{.CreatedAt}} · модель {{.Model}} · temperature {{.Temperature}} · provider tokens {{.Tokens}}</p></header>
<section><h2>1. Цель и отличие от Day 11</h2><p>Memory: «Что агент знает / помнит?» Personalization: «Как агент должен работать с этим пользователем?» Личная память о краткости становится структурированным detail_level=brief; Context Builder применяет настройку до генерации.</p><p>Ниже сохранены настоящие ответы одной модели, без ручной правки и без второго LLM-переписывания. Табличный Markdown показан дословно для проверяемости.</p></section>
<section><h2>2. Архитектура UserProfile</h2><pre>USER → USER PROFILE (style / format / detail / constraints)
CURRENT MESSAGE ───┐
SHORT-TERM ────────┤
WORKING MEMORY ────┤
LONG-TERM MEMORY ──┼→ CONTEXT BUILDER → LLM → PERSONALIZED RESPONSE
DOMAIN DATA ───────┤
USER PROFILE ─────┘</pre><p>User остаётся identity. UserProfile — типизированный вид существующего user_preferences.settings_json, ключ user_id. Отдельной таблицы или конкурирующей копии профиля нет. Audit Log хранит provenance и trace.</p></section>
<section><h2>3. Поля и значения по умолчанию</h2><p>style=neutral; detail=normal; language=ru; format=text; summary_first=false; confirmation=confirm_destructive; hide_llm_details=false. Подтверждения BOM и производства обязательны. Язык en можно хранить, но текущая генерация/UI работают на русском.</p><h2>4. Подключение к запросам</h2><p>AgentContextBuilder добавляет компактный [USER PROFILE] перед генерацией; обычный интерпретатор и Telegram semantic pipeline получают тот же блок. Отчёты читают SQLite и делают один Complete-вызов. Локальные формы не обращаются к LLM. Все профили разрешаются заново по внутреннему user_id.</p></section>
<section class="grid"><div><h2>5. Profile A</h2><p>neutral · brief · list · summary_first=true</p></div><div><h2>6. Profile B</h2><p>technical · detailed · table · summary_first=false</p></div></section>
<section><h2>7. Один запрос — разные ответы</h2><p>A/B выполнено с одинаковыми user/session, входным запросом и доменным snapshot, без накопления истории между вызовами; меняется только профиль. Изоляция второго пользователя проверяется отдельно. Факты: Гипс 10 кг / минимум 2 кг; Кисточки 400 шт / минимум 50; Коробки 100 шт / минимум 20.</p><p>StocksPresent — только автоматическая проверка наличия названий/чисел, не доказательство полной семантической точности ответа. Тексты доступны для ручной проверки ниже.</p></section>
{{range .Cases}}<article><h2>{{.Name}}</h2><p><b>Одинаковый вход / текущий запрос:</b> {{.Input}}</p><p>Факты найдены автоматической проверкой: {{.StocksPresent}} · профиль: {{.Context.Profile.Applied.Detail}} / {{.Context.Profile.Applied.Format}} · overhead ≈{{.Context.Profile.EstimatedTokens}} tokens</p><div class="answer">{{.Response}}</div>{{if .Error}}<p>{{.Error}}</p>{{end}}<details><summary>Точный контекст, отправленный LLM</summary><pre>{{.Context.Prompt}}</pre></details></article>{{end}}
<section><h2>8–10. Автоматическое применение и изменения</h2><p>Новая сессия получает сохранённый профиль без напоминания. «В этот раз объясни подробно» меняет только effective detail для текущего запроса. Следующий запрос возвращается к brief. «Теперь всегда отвечай подробно» записывает настройку; новая сессия сохраняет detailed.</p><h2>11. Изоляция и Workshop</h2>{{range $k,$v:=.Checks}}<p><b>{{$k}}</b>: {{$v}}</p>{{end}}<h2>12. Авторизация и приоритеты</h2><p>Security / authorization / business rules &gt; явный текущий запрос &gt; требования задачи &gt; профиль &gt; личная память &gt; defaults. Preferences не выдают permissions и не отменяют обязательные кнопки подтверждения. Эти границы проверяются интеграционными тестами с тестовым транспортом; live-запросы ничего не изменяют в домене.</p><h2>13. Вывод и ограничения</h2><p>Персонализация подключена до генерации, сохраняется между сессиями и мастерскими. В отчёте можно сравнить фактические различия оформления. Token overhead — оценка UTF-8 bytes / 3, usage — данные провайдера. Естественные команды настроек ограничены явными поддержанными фразами. Настройки не меняют обязательный UX складских форм. Workshop overrides не введены. Day 13 не запускается.</p></section><footer>Автономный HTML без CDN. Исходные данные: results.json; учебная БД: fixture.db. Рабочая база и Telegram не использовались.</footer></main></html>`
