package experiment

import (
	"context"
	"encoding/json"
	"errors"
	"html/template"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"workshop-agent/internal/background"
	"workshop-agent/internal/integrations/mcpclient"
	"workshop-agent/internal/integrations/mcpmanager"
	"workshop-agent/internal/integrations/wbmcpfixture"
	"workshop-agent/internal/workshops"
)

type day18Sender struct{ Messages []string }

func (s *day18Sender) Send(_ context.Context, _, _ int64, text string) error {
	s.Messages = append(s.Messages, text)
	return nil
}
func RunDay18(ctx context.Context) (string, error) {
	dir := filepath.Join("reports", "day18-background-jobs", time.Now().UTC().Format("20060102T150405.000000000Z"))
	if e := os.MkdirAll(dir, 0755); e != nil {
		return "", e
	}
	ws := workshops.NewService(filepath.Join(dir, "fixture.db"))
	defer ws.Close()
	u, e := ws.UpsertUser(991801, "", "Day18 fixture", "")
	if e != nil {
		return "", e
	}
	w, e := ws.CreateOwnedWorkshop(u, "Day18 isolated workshop")
	if e != nil {
		return "", e
	}
	if _, e = ws.DB().Exec(`INSERT INTO marketplace_connections(id,workshop_id,enabled,seller_id) VALUES(1,?,1,'day17-fixture')`, w); e != nil {
		return "", e
	}
	sender := &day18Sender{}
	apiCalls := &atomic.Int64{}
	api := &wbmcpfixture.API{Mode: "success", Calls: apiCalls}
	s := background.New(ws.DB(), nil, sender)
	manager, e := mcpmanager.New(ctx, api, s)
	if e != nil {
		return "", e
	}
	client := manager.Client
	defer manager.Close()
	s.WB.MCP = client
	s.Now = func() time.Time { return time.Date(2026, 9, 24, 6, 0, 0, 0, time.UTC) }
	scheduled, e := manager.ScheduleWB(ctx, u, w, mcpmanager.ScheduleInput{LocalTime: "08:00", Timezone: "Europe/Moscow", NotificationEnabled: true})
	if e != nil || scheduled.JobID == 0 {
		return "", errors.New("schedule MCP failed")
	}
	j, e := s.Get(u, w)
	if e != nil {
		return "", e
	}
	if e = s.RunNow(ctx, u, w); e != nil {
		return "", e
	}
	// Restart the scheduler facade over persisted jobs, retaining the same MCP
	// session across runs. A full process restart is covered by persistence tests.
	api.Mode = "changed"
	second := client
	restarted := background.New(ws.DB(), second, sender)
	restarted.Now = func() time.Time { return time.Date(2026, 9, 27, 6, 0, 0, 0, time.UTC) }
	if e = restarted.Tick(ctx); e != nil {
		return "", e
	}
	if e = restarted.Tick(ctx); e != nil {
		return "", e
	}
	j, e = restarted.Get(u, w)
	if e != nil {
		return "", e
	}
	var runs, snapshots, diffs int
	var raw string
	if e = ws.DB().QueryRow(`SELECT COUNT(*) FROM background_job_runs`).Scan(&runs); e != nil {
		return "", e
	}
	if e = ws.DB().QueryRow(`SELECT COUNT(*) FROM wb_daily_snapshots`).Scan(&snapshots); e != nil {
		return "", e
	}
	if e = ws.DB().QueryRow(`SELECT COUNT(*) FROM wb_daily_diffs`).Scan(&diffs); e != nil {
		return "", e
	}
	if e = ws.DB().QueryRow(`SELECT aggregate_json FROM background_job_runs ORDER BY id DESC LIMIT 1`).Scan(&raw); e != nil {
		return "", e
	}
	var a background.Aggregate
	if e = json.Unmarshal([]byte(raw), &a); e != nil {
		return "", e
	}
	beforeSummary := apiCalls.Load()
	storedSummary, e := manager.DailySummary(ctx, u, w)
	if e != nil {
		return "", e
	}
	summaryWBCalls := apiCalls.Load() - beforeSummary
	if storedSummary.Aggregate == nil || *storedSummary.Aggregate != a || summaryWBCalls != 0 {
		return "", errors.New("stored summary MCP acceptance failed")
	}
	summaryJSON, _ := json.MarshalIndent(storedSummary, "", "  ")
	if e = os.WriteFile(filepath.Join(dir, "summary-mcp.json"), summaryJSON, 0600); e != nil {
		return "", e
	}
	if e = second.Close(); e != nil {
		return "", e
	}
	passed := runs == 2 && snapshots == 8 && diffs == 2 && len(sender.Messages) == 2 && a.PriceChanges == 1 && a.StockChanges == 1 && a.ZeroStock == 1 && a.LowStock == 2 && a.Errors == 0 && second.State().ServerClosed
	r := struct {
		StoredSummary          background.DailySummary
		SummaryWBCalls         int64
		Passed                 bool
		Mode                   string
		Job                    background.Job
		Aggregate              background.Aggregate
		Runs, Snapshots, Diffs int
		Messages               []string
		Transport              mcpclient.Discovery
	}{storedSummary, summaryWBCalls, passed, "MOCK WB + actual MCP in-memory; Telegram captured, not sent; clock advanced for catch-up", j, a, runs, snapshots, diffs, sender.Messages, second.State()}
	data, _ := json.MarshalIndent(r, "", "  ")
	if e = os.WriteFile(filepath.Join(dir, "result.json"), data, 0600); e != nil {
		return "", e
	}
	t := template.Must(template.New("report").Parse(`<!doctype html><html lang="ru"><meta charset="utf-8"><title>Day18 WB daily sync</title><style>body{font:16px system-ui;max-width:1000px;margin:32px auto;padding:20px}pre{white-space:pre-wrap;background:#eef3f6;padding:16px}td,th{padding:8px;text-align:left}</style><h1>Day18 · WB_DAILY_SYNC</h1><p>Passed: {{.Passed}}. {{.Mode}}</p><pre>08:00 Workshop timezone → persistent BackgroundJob → Scheduler
→ existing MCP Client → in-memory → existing WB MCP Server
→ WB API Client (mock here) → SQLite snapshots/current
→ diff/aggregate → Telegram summary (captured here)</pre><table><tr><th>Job / Workshop</th><td>{{.Job.ID}} / {{.Job.WorkshopID}}</td></tr><tr><th>Schedule / Timezone</th><td>{{.Job.Schedule}} {{.Job.LocalTime}} / {{.Job.Timezone}}</td></tr><tr><th>Last / Next (UTC Unix)</th><td>{{.Job.LastRun}} / {{.Job.NextRun}}</td></tr><tr><th>Status</th><td>{{.Job.LastResult}}; job={{.Job.Status}}</td></tr><tr><th>Price MCP tool</th><td>wb_get_prices → wildberries.Client.Prices</td></tr><tr><th>Stock MCP tool</th><td>wb_get_wb_stocks → wildberries.Client.WBStocks</td></tr><tr><th>Products / Price changes / Stock changes</th><td>{{.Aggregate.ProductsCount}} / {{.Aggregate.PriceChanges}} / {{.Aggregate.StockChanges}}</td></tr><tr><th>Zero / Low / Errors</th><td>{{.Aggregate.ZeroStock}} / {{.Aggregate.LowStock}} / {{.Aggregate.Errors}}</td></tr><tr><th>Runs / Snapshot rows / Diffs</th><td>{{.Runs}} / {{.Snapshots}} / {{.Diffs}}</td></tr><tr><th>Restart/catch-up</th><td>Scheduler recreated; same MCP session reused; advanced three days, two ticks: one additional run. Passed={{.Passed}}</td></tr><tr><th>Cleanup</th><td>Session={{.Transport.SessionClosed}}, server={{.Transport.ServerClosed}}, write tools={{.Transport.WriteToolsExposed}}</td></tr></table>{{range .Messages}}<pre>{{.}}</pre>{{end}}<h2>RAW/SNAPSHOT DATA → AGGREGATE → SUMMARY</h2>
<p>Сохранять данные: SQLite wb_daily_snapshots ({{.Snapshots}} строк), BackgroundJobRun.aggregate_json.</p>
<p>Выполняться по расписанию: WB_DAILY_SYNC, DAILY_AT_TIME {{.Job.LocalTime}}, {{.Job.Timezone}}.</p>
<p>Возвращать агрегированный результат: MCP wb_get_daily_summary → сохранённый aggregate_json → typed structured result.</p>
<p>Регулярный summary: уведомления после runs показаны выше. Запрос последней сводки не запускает run повторно.</p>
<pre>MCP TRACE
Source: Последняя сводка (offline application adapter)
Tool: wb_get_daily_summary
Transport: in-memory
Source data: BackgroundJobRun.aggregate_json
Run ID: {{.StoredSummary.RunID}}
Result: {{.StoredSummary.Status}}
WB API CALLS: {{.SummaryWBCalls}}</pre>
<p><a href="summary-mcp.json">Фактический structured MCP result</a></p>
<h3>Telegram presentation (captured, not sent)</h3><pre>{{.StoredSummary.Summary}}</pre>
<p>Offline evidence, not live WB validation. No real messages sent. <a href="result.json">Actual result JSON</a>.</p></html>`))
	path := filepath.Join(dir, "report.html")
	f, e := os.Create(path)
	if e != nil {
		return "", e
	}
	e = t.Execute(f, r)
	ce := f.Close()
	if e != nil {
		return path, e
	}
	if ce != nil {
		return path, ce
	}
	if !passed {
		return path, errors.New("day18 mock acceptance failed")
	}
	return path, nil
}
