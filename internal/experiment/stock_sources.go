package experiment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"time"
	"workshop-agent/internal/background"
	"workshop-agent/internal/integrations/mcpmanager"
	"workshop-agent/internal/integrations/wbmcpfixture"
	"workshop-agent/internal/marketplace"
	"workshop-agent/internal/workshops"
)

func RunStockSources(ctx context.Context) (string, error) {
	tmp, e := os.MkdirTemp("", "stock-sources-")
	if e != nil {
		return "", e
	}
	defer os.RemoveAll(tmp)
	ws := workshops.NewService(filepath.Join(tmp, "fixture.db"))
	defer ws.Close()
	u, e := ws.UpsertUser(991962, "", "Stock sources fixture", "")
	if e != nil {
		return "", e
	}
	w, e := ws.CreateOwnedWorkshop(u, "Stock sources fixture")
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
	if e = jobs.RunNow(ctx, u, w); e != nil {
		return "", e
	}
	first, e := jobs.LastRun(w, j.ID)
	if e != nil || first.Status != "success" {
		return "", errors.New("independent success failed")
	}
	now = now.AddDate(0, 0, 1)
	api.Mode = "stock_sources"
	if e = jobs.RunNow(ctx, u, w); e != nil {
		return "", e
	}
	last, e := jobs.LastRun(w, j.ID)
	if e != nil {
		return "", e
	}
	var result background.PipelineResult
	if json.Unmarshal([]byte(last.ResultJSON), &result) != nil || result.Status != "PARTIAL_SUCCESS" || result.SaveResult.SellerRecords != 33 || result.Aggregate.WBSource.Status != "PERMISSION_DENIED" {
		return "", errors.New("partial acceptance failed")
	}
	var seller, wbCount int
	_ = ws.DB().QueryRow(`SELECT COUNT(*) FROM marketplace_stocks WHERE kind='seller_stocks'`).Scan(&seller)
	_ = ws.DB().QueryRow(`SELECT COUNT(*) FROM marketplace_stocks WHERE kind='wb_stocks'`).Scan(&wbCount)
	if seller != 33 || wbCount != 3 {
		return "", errors.New("source retention failed")
	}
	service, e := marketplace.New(ws.DB(), api)
	if e != nil {
		return "", e
	}
	defer service.Close()
	view, e := service.StockSourcesText(marketplace.Scope{UserID: u, WorkshopID: w, ConnectionID: 1}, "stocks", 0)
	if e != nil {
		return "", e
	}
	dir := filepath.Join("reports", "wb-stock-sources", time.Now().UTC().Format("20060102T150405.000000000Z"))
	if e = os.MkdirAll(dir, 0755); e != nil {
		return "", e
	}
	raw, _ := json.MarshalIndent(result, "", "  ")
	if e = os.WriteFile(filepath.Join(dir, "result.json"), raw, 0600); e != nil {
		return "", e
	}
	data := struct{ Trace, UI, JSON string }{background.PipelineTrace(result), view, string(raw)}
	f, e := os.Create(filepath.Join(dir, "report.html"))
	if e != nil {
		return "", e
	}
	e = stockSourcesTemplate.Execute(f, data)
	ce := f.Close()
	if e != nil {
		return "", e
	}
	if ce != nil {
		return "", ce
	}
	fmt.Println(data.Trace)
	fmt.Println("PASS: received=34 valid=33 invalid=1 saved=33; WB=403; previous WB rows retained=3; real in-memory MCP, mock API.")
	return filepath.Join(dir, "report.html"), nil
}

var stockSourcesTemplate = template.Must(template.New("stocks").Parse(`<!doctype html><html lang="ru"><meta charset="utf-8"><title>Wildberries · Независимые источники остатков</title><style>body{font:16px system-ui;max-width:1100px;margin:40px auto;padding:20px;background:#122032;color:#e9f1ff}pre,table{background:#20324b;padding:18px;border-radius:8px}pre{white-space:pre-wrap}td,th{padding:12px;text-align:left}a{color:#80ddc6}</style><h1>Wildberries · Независимые источники остатков</h1><p>WA-D162 / migration117 · PASS. Реальный in-memory MCP; mock WB; временная SQLite. Production .env/БД и live API не использовались.</p><pre>INTERNAL: склад мастерской — независим
SELLER: wb_get_seller_stocks → SellerStockBatch → marketplace-api
WB: wb_get_wb_stocks → WBStocks → seller-analytics-api
Никаких автоматических сумм и взаимного перезаписывания.
prices / SELLER / WB (независимые результаты)
→ summary с provenance → атомарный save доступных данных
→ PARTIAL_SUCCESS</pre><table><tr><th>Источник</th><th>Endpoint / требования</th><th>Текущий BASE token</th></tr><tr><td>SELLER</td><td>GET /api/v3/warehouses + POST /api/v3/stocks/{warehouseId}; Marketplace. В разделе нет отдельного запрета BASE. Stocks300/min,200ms,burst20; общий limiter сохранён консервативным.</td><td>YES: ранее наблюдались реальные HTTP200, 25.09.2026 15:24МСК.</td></tr><tr><td>WB</td><td>POST /api/analytics/v1/stocks-report/wb-warehouses; Personal/Service + Analytics;3/min,20s,burst1.</td><td>NO: BASE не указан среди разрешённых; ранее наблюдался403.</td></tr></table><p>Сверка25.09.2026: <a href="https://dev.wildberries.ru/openapi/work-with-products">WB Products</a>, <a href="https://dev.wildberries.ru/release-notes?id=272">уведомление WB</a>, <a href="https://dev.wildberries.ru/openapi/analytics">Analytics</a>. Прямое открытие вернуло498; использованы индексированные официальные страницы. Актуальность недоступного содержимого нельзя гарантировать. Токен не менялся, новая live-проверка не выполнялась.</p><h2>Фактический mock pipeline trace</h2><p>SELLER:34 получено,33 валидны и сохранены,1 diagnostic rejection. WB:403, новых строк0,3 прежние строки сохранены. Полный baseline обоих источников был сохранён до partial run.</p><pre>{{.Trace}}</pre><h2>Telegram view</h2><pre>{{.UI}}</pre><h2>Структурированный результат</h2><pre>{{.JSON}}</pre><h2>Старая ошибка invalid_response</h2><p>Подтверждён дефект старого алгоритма: любой invalid/missing record прекращал обработку, а finish при ошибке не сохранял уже полученные записи. Точную строку/ветку старого ответа восстановить нельзя: raw response не сохранялся. Теперь есть record index/ID, field, expected, actual type, missing counter; секреты и сырые значения не пишутся.</p></html>`))
