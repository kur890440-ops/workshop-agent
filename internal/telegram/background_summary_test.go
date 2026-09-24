package telegram

import (
	"context"
	"strings"
	"testing"
	"workshop-agent/internal/background"
	"workshop-agent/internal/integrations/mcpmanager"
	"workshop-agent/internal/integrations/wbmcpfixture"
)

func TestTelegramLastSummaryUsesMCP(t *testing.T) {
	h, u, w := wbFixture(t)
	jobs := background.New(h.bot.WS.DB(), nil, nil)
	h.bot.Background = jobs
	m, e := mcpmanager.New(context.Background(), wbmcpfixture.API{Mode: "missing"}, jobs)
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	h.bot.DailySummary = m
	h.message(t, 900001, "/wb_auto summary")
	if !strings.Contains(h.sent[len(h.sent)-1]["text"].(string), "еще не выполнялась") {
		t.Fatal(h.sent)
	}
	if _, e = h.bot.WS.DB().Exec(`INSERT INTO marketplace_connections(id,workshop_id,enabled,seller_id) VALUES(1,?,1,'fixture')`, w); e != nil {
		t.Fatal(e)
	}
	j, e := jobs.Create(u, w)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = h.bot.WS.DB().Exec(`INSERT INTO background_job_runs(job_id,workshop_id,job_type,local_date,status,started_at,finished_at,lease_owner,lease_until,aggregate_json) VALUES(?,?,'WB_DAILY_SYNC','2026-09-24','success',100,101,'fixture',0,'{"products_count":37,"price_changes_count":0,"stock_changes_count":0,"zero_stock_count":0,"low_stock_count":0,"errors_count":0,"prices_ok":true,"stocks_ok":true}')`, j.ID, w); e != nil {
		t.Fatal(e)
	}
	h.message(t, 900001, "/wb_auto summary trace")
	text := h.sent[len(h.sent)-1]["text"].(string)
	if !strings.Contains(text, "Проверено товаров: 37") || !strings.Contains(text, "wb_get_daily_summary") || !strings.Contains(text, "WB API CALLS: 0") {
		t.Fatal(text)
	}
	h.message(t, 900001, "/wb")
	h.click(t, 900001, "Последняя сводка")
	if !strings.Contains(h.sent[len(h.sent)-1]["text"].(string), "Проверено товаров: 37") {
		t.Fatal(h.sent)
	}
	// Closing the real MCP session must break this flow despite available SQL data.
	if e = m.Close(); e != nil {
		t.Fatal(e)
	}
	h.message(t, 900001, "/wb_auto summary")
	if !strings.Contains(h.sent[len(h.sent)-1]["text"].(string), "Не удалось получить") {
		t.Fatal("Telegram bypassed MCP")
	}
}
