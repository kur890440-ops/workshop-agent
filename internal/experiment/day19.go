package experiment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"reflect"
	"time"
	"workshop-agent/internal/background"
	"workshop-agent/internal/integrations/mcpclient"
	"workshop-agent/internal/integrations/mcpmanager"
	"workshop-agent/internal/integrations/wbmcpfixture"
	"workshop-agent/internal/workshops"
)

// RunDay19 exercises real in-memory MCP with synthetic WB data and temporary DB.
// It never reads configuration, opens the production DB or starts Telegram.
func RunDay19(ctx context.Context) (string, error) {
	tmp, e := os.MkdirTemp("", "workshop-day19-")
	if e != nil {
		return "", e
	}
	defer os.RemoveAll(tmp)
	ws := workshops.NewService(filepath.Join(tmp, "fixture.db"))
	defer ws.Close()
	u, e := ws.UpsertUser(991919, "", "Day19 fixture", "")
	if e != nil {
		return "", e
	}
	w, e := ws.CreateOwnedWorkshop(u, "Day19 isolated workshop")
	if e != nil {
		return "", e
	}
	if _, e = ws.DB().Exec(`INSERT INTO marketplace_connections(id,workshop_id,enabled,seller_id) VALUES(1,?,1,'day17-fixture')`, w); e != nil {
		return "", e
	}
	api := &wbmcpfixture.API{Mode: "success"}
	jobs := background.New(ws.DB(), nil, nil)
	now := time.Date(2026, 9, 25, 6, 0, 0, 0, time.UTC)
	jobs.Now = func() time.Time { return now }
	manager, e := mcpmanager.New(ctx, api, jobs)
	if e != nil {
		return "", e
	}
	defer manager.Close()
	jobs.WB.MCP = manager.Client
	j, e := jobs.Create(u, w)
	if e != nil {
		return "", e
	}
	read := func() (background.PipelineResult, error) {
		var p background.PipelineResult
		r, e := jobs.LastRun(w, j.ID)
		if e != nil {
			return p, e
		}
		e = json.Unmarshal([]byte(r.ResultJSON), &p)
		return p, e
	}
	if e = jobs.RunNow(ctx, u, w); e != nil {
		return "", e
	}
	baseline, e := read()
	if e != nil || baseline.Status != "SUCCESS" {
		return "", errors.New("baseline pipeline failed")
	}
	now = now.AddDate(0, 0, 1)
	api.Mode = "changed"
	if e = jobs.Tick(ctx); e != nil {
		return "", e
	}
	success, e := read()
	if e != nil || success.Status != "SUCCESS" || success.Aggregate.PriceChanges != 1 || success.Aggregate.StockChanges != 1 || success.SaveResult.PriceRecords != 1 || success.SaveResult.StockRecords != 3 {
		return "", errors.New("pipeline data transfer acceptance failed")
	}
	stored, e := manager.DailySummary(ctx, u, w)
	if e != nil || stored.Aggregate == nil || !reflect.DeepEqual(*stored.Aggregate, *success.Aggregate) {
		return "", errors.New("stored summary mismatch")
	}
	now = now.AddDate(0, 0, 1)
	api.Mode = "api"
	if e = jobs.RunNow(ctx, u, w); e != nil {
		return "", e
	}
	failed, e := read()
	if e != nil || failed.Status != "PARTIAL_SUCCESS" || len(failed.Steps) != 5 || failed.Steps[0].Status != "SUCCESS" || failed.Steps[1].Status != "SUCCESS" || failed.Steps[2].Status != "FAILED" || failed.Steps[3].Status != "SUCCESS" || failed.Steps[4].Status != "SUCCESS" {
		return "", errors.New("strict failure acceptance failed")
	}
	var snapshots int
	if e = ws.DB().QueryRow(`SELECT COUNT(*) FROM wb_daily_snapshots`).Scan(&snapshots); e != nil || snapshots != 12 {
		return "", errors.New("invalid snapshot persisted")
	}
	after, e := manager.DailySummary(ctx, u, w)
	if e != nil || after.RunID == stored.RunID {
		return "", errors.New("failed run replaced summary")
	}
	dir := filepath.Join("reports", "day19-mcp-composition", time.Now().UTC().Format("20060102T150405.000000000Z"))
	if e = os.MkdirAll(dir, 0755); e != nil {
		return "", e
	}
	data := struct {
		Success, Failed background.PipelineResult
		Discovery       mcpclient.Discovery
		Snapshots       int
	}{success, failed, manager.Client.State(), snapshots}
	raw, e := json.MarshalIndent(data, "", "  ")
	if e != nil {
		return "", e
	}
	if e = os.WriteFile(filepath.Join(dir, "result.json"), raw, 0600); e != nil {
		return "", e
	}
	view := struct{ Success, Failure, JSON, Morning string }{background.PipelineTrace(success), background.PipelineTrace(failed), string(raw), stored.Summary}
	path := filepath.Join(dir, "report.html")
	f, e := os.Create(path)
	if e != nil {
		return "", e
	}
	e = day19Template.Execute(f, view)
	closeErr := f.Close()
	if e != nil {
		return "", e
	}
	if closeErr != nil {
		return "", closeErr
	}
	fmt.Println(view.Success)
	fmt.Println(view.Failure)
	return path, nil
}

var day19Template = template.Must(template.New("day19").Parse(`<!doctype html><html lang="ru"><meta charset="utf-8"><title>День 19 · Композиция MCP-инструментов</title><style>body{font:16px system-ui;max-width:1100px;margin:40px auto;padding:20px;background:#101b2a;color:#e5eef9}pre{white-space:pre-wrap;background:#1c2e43;padding:20px;border-radius:10px}h1,h2{color:#83d9c5}</style><h1>День 19 · Композиция MCP-инструментов</h1><p>Автоматический WB Market Sync Pipeline в Workshop Agent</p><p>PASS — реальные MCP initialize/ListTools/CallTool, in-memory; фиктивный WB API и временная SQLite. Это не проверка live Wildberries. Один process/executable. WB_WRITE=false.</p><h2>Композиция</h2><pre>BackgroundScheduler (08:00) / Run Now
↓ WB_DAILY_SYNC
↓ MCPPipelineRunner: WB_MARKET_SYNC_PIPELINE
↓ wb_get_prices → Prices
↓ wb_get_seller_stocks → SellerStockBatch
↓ wb_get_wb_stocks → WBStocks
↓ wb_build_market_summary → BuildMarketSummary (LOCAL_COMPUTE)
↓ wb_save_market_snapshot → SaveMarketSnapshot (LOCAL_WRITE, WB_WRITE=false)
↓ PipelineResult → BackgroundJobRun.result_json → Telegram summary</pre><h2>Реальный successful trace и передача данных</h2><p>Первый запуск создаёт baseline, следующий плановый запуск меняет цену 10000→12000 и остаток 12→3. Aggregate передаётся через MCP в save. Сохранённая сводка читается отдельным Day18 tool.</p><pre>{{.Success}}</pre><h2>Контролируемый отказ stock tool</h2><p>Независимые цены и SELLER сохранены, WB недоступен. WA-D162 заменяет strict-политику: PARTIAL_SUCCESS, snapshots=12.</p><pre>{{.Failure}}</pre><h2>Утренняя сводка</h2><p>SELLER rows → product + variant → сумма складов SELLER → normalized aggregate → Telegram.</p><pre>{{.Morning}}</pre><h2>Воспроизводимые структурированные результаты и ListTools</h2><pre>{{.JSON}}</pre></html>`))
