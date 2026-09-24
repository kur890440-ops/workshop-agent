package mcpmanager

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"workshop-agent/internal/background"
	"workshop-agent/internal/integrations/wbmcpfixture"
	"workshop-agent/internal/workshops"
)

func TestStoredSummaryThroughMCP(t *testing.T) {
	ctx := context.Background()
	ws := workshops.NewService(filepath.Join(t.TempDir(), "summary.db"))
	defer ws.Close()
	u, e := ws.UpsertUser(991877, "", "Owner", "")
	if e != nil {
		t.Fatal(e)
	}
	w, e := ws.CreateOwnedWorkshop(u, "A")
	if e != nil {
		t.Fatal(e)
	}
	jobs := background.New(ws.DB(), nil, nil)
	api := &countedAPI{API: wbmcpfixture.API{Mode: "missing"}}
	m, e := New(ctx, api, jobs)
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	found := false
	for _, tool := range m.Client.State().Tools {
		if tool.Name == SummaryTool {
			found = true
			if !tool.ReadOnly || !strings.Contains(string(tool.OutputSchema), "products_count") {
				t.Fatal(tool)
			}
		}
	}
	if !found {
		t.Fatal("not listed")
	}
	out, e := m.DailySummary(ctx, u, w)
	if e != nil || out.Code != "NO_SUMMARY_AVAILABLE" || out.Aggregate != nil {
		t.Fatal(out, e)
	}
	if _, e = ws.DB().Exec(`INSERT INTO marketplace_connections(id,workshop_id,enabled,seller_id) VALUES(1,?,1,'fixture')`, w); e != nil {
		t.Fatal(e)
	}
	j, e := jobs.Create(u, w)
	if e != nil {
		t.Fatal(e)
	}
	out, e = m.DailySummary(ctx, u, w)
	if e != nil || out.Code != "NO_SUMMARY_AVAILABLE" {
		t.Fatal(out, e)
	}
	insert := func(date, status string, finished int, aggregate, result string) int64 {
		t.Helper()
		r, e := ws.DB().Exec(`INSERT INTO background_job_runs(job_id,workshop_id,job_type,local_date,status,started_at,finished_at,lease_owner,lease_until,aggregate_json,result_json) VALUES(?,?,'WB_DAILY_SYNC',?,?,1,?,'fixture',0,?,?)`, j.ID, w, date, status, finished, aggregate, result)
		if e != nil {
			t.Fatal(e)
		}
		id, _ := r.LastInsertId()
		return id
	}
	aggregate := `{"products_count":80,"price_changes_count":4,"stock_changes_count":11,"zero_stock_count":3,"low_stock_count":7,"errors_count":0,"prices_ok":true,"stocks_ok":true,"token":"synthetic-secret"}`
	first := insert("2026-09-20", "success", 100, aggregate, `{}`)
	out, e = m.DailySummary(ctx, u, w)
	if e != nil || out.RunID != first || out.Aggregate.ProductsCount != 80 || out.Aggregate.PriceChanges != 4 {
		t.Fatal(out, e)
	}
	// Newer failed runs do not hide the last usable result.
	insert("2026-09-21", "failed", 200, `{}`, `{}`)
	out, e = m.DailySummary(ctx, u, w)
	if e != nil || out.RunID != first {
		t.Fatal(out, e)
	}
	partial := insert("2026-09-22", "partial_success", 300, `{"products_count":9,"price_changes_count":2,"stock_changes_count":0,"zero_stock_count":0,"low_stock_count":0,"errors_count":1,"prices_ok":true,"stocks_ok":false}`, `{"wb_get_wb_stocks":"wb_rate_limit","unknown":"synthetic-secret"}`)
	out, e = m.DailySummary(ctx, u, w)
	if e != nil || out.RunID != partial || out.Status != "partial_success" || out.Aggregate.Errors != 1 || !strings.Contains(out.Summary, "неполные") {
		t.Fatal(out, e)
	}
	raw, _ := json.Marshal(out)
	if strings.Contains(string(raw), "synthetic-secret") || !strings.Contains(string(raw), "wb_rate_limit") {
		t.Fatal(string(raw))
	}
	if api.configured.Load() != 0 || api.prices.Load() != 0 || api.stocks.Load() != 0 || api.sellers.Load() != 0 {
		t.Fatal("summary performed live WB calls")
	}
	if _, e = m.DailySummary(ctx, u+999, w); e == nil {
		t.Fatal("foreign user")
	}
	b, e := ws.CreateOwnedWorkshop(u, "B")
	if e != nil {
		t.Fatal(e)
	}
	if e = ws.SetActiveWorkshop(u, b); e != nil {
		t.Fatal(e)
	}
	if _, e = m.DailySummary(ctx, u, w); e == nil {
		t.Fatal("stale workshop")
	}
	out, e = m.DailySummary(ctx, u, b)
	if e != nil || out.Code != "NO_SUMMARY_AVAILABLE" || out.RunID != 0 {
		t.Fatal("cross-workshop result", out, e)
	}
	// A read grant cannot authorize the scheduling mutation.
	m.mu.Lock()
	m.grants["read-only-grant"] = scope{u, b, time.Now().Add(time.Minute), SummaryTool}
	m.mu.Unlock()
	if e = m.Client.Schedule(ctx, "read-only-grant", ScheduleInput{LocalTime: "08:00", Timezone: "UTC"}, &ScheduleOutput{}); e == nil {
		t.Fatal("read grant used for mutation")
	}
	if e = m.Client.DailySummary(ctx, "forged", &out); e == nil {
		t.Fatal("forged grant")
	}
	if e = ws.SetActiveWorkshop(u, w); e != nil {
		t.Fatal(e)
	}
	if _, e = ws.DB().Exec(`UPDATE workshop_members SET role='VIEWER' WHERE user_id=? AND workshop_id=?`, u, w); e != nil {
		t.Fatal(e)
	}
	if _, e = m.DailySummary(ctx, u, w); e != nil {
		t.Fatal("read permission denied", e)
	}
	if _, e = ws.DB().Exec(`UPDATE workshop_members SET is_active=0 WHERE user_id=? AND workshop_id=?`, u, w); e != nil {
		t.Fatal(e)
	}
	if _, e = m.DailySummary(ctx, u, w); e == nil {
		t.Fatal("revoked membership")
	}
}
