package mcpmanager

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"workshop-agent/internal/background"
	"workshop-agent/internal/integrations/wbmcpfixture"
	wb "workshop-agent/internal/marketplace/wildberries"
	"workshop-agent/internal/workshops"
)

type countedAPI struct {
	wbmcpfixture.API
	prices, stocks, sellers, configured atomic.Int32
}

func (a *countedAPI) Configured() bool { a.configured.Add(1); return a.API.Configured() }
func (a *countedAPI) Seller(ctx context.Context) (wb.Seller, error) {
	a.sellers.Add(1)
	return a.API.Seller(ctx)
}
func (a *countedAPI) Prices(ctx context.Context) ([]wb.Price, error) {
	a.prices.Add(1)
	return a.API.Prices(ctx)
}
func (a *countedAPI) WBStocks(ctx context.Context) ([]wb.Stock, error) {
	a.stocks.Add(1)
	return a.API.WBStocks(ctx)
}

func TestSchedulingToolScopeSchemasAndExecution(t *testing.T) {
	ctx := context.Background()
	ws := workshops.NewService(filepath.Join(t.TempDir(), "jobs.db"))
	defer ws.Close()
	u, e := ws.UpsertUser(991899, "", "Owner", "")
	if e != nil {
		t.Fatal(e)
	}
	w, e := ws.CreateOwnedWorkshop(u, "Scope")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = ws.DB().Exec(`INSERT INTO marketplace_connections(id,workshop_id,enabled,seller_id) VALUES(1,?,1,'day17-fixture')`, w); e != nil {
		t.Fatal(e)
	}
	api := &countedAPI{API: wbmcpfixture.API{Mode: "success"}}
	jobs := background.New(ws.DB(), nil, nil)
	jobs.Now = func() time.Time { return time.Date(2026, 9, 24, 6, 0, 0, 0, time.UTC) }
	manager, e := New(ctx, api, jobs)
	if e != nil {
		t.Fatal(e)
	}
	defer manager.Close()
	jobs.WB.MCP = manager.Client
	state := manager.Client.State()
	if state.Transport != "in-memory" || len(state.Tools) != 8 || state.WriteToolsExposed != 0 || state.LocalMutationTools != 1 {
		t.Fatal(state)
	}
	found := false
	for _, tool := range state.Tools {
		if tool.Name == ScheduleTool {
			found = true
			if !strings.Contains(string(tool.OutputSchema), "next_run_at") || !strings.Contains(string(tool.OutputSchema), "job_id") || tool.ReadOnly || !strings.Contains(string(tool.InputSchema), "notification_enabled") || strings.Contains(string(tool.InputSchema), "workshop_id") {
				t.Fatal(tool)
			}
		}
	}
	if !found {
		t.Fatal("missing scheduling tool")
	}
	in := ScheduleInput{LocalTime: "08:00", Timezone: "Europe/Moscow", NotificationEnabled: false}
	var output ScheduleOutput
	if e = manager.Client.Schedule(ctx, "forged", in, &output); e == nil {
		t.Fatal("missing grant accepted")
	}
	if _, e = manager.ScheduleWB(ctx, u+999, w, in); e == nil {
		t.Fatal("foreign user")
	}
	if _, e = manager.ScheduleWB(ctx, u, w+999, in); e == nil {
		t.Fatal("foreign workshop")
	}
	bad := in
	bad.Timezone = "Local"
	if _, e = manager.ScheduleWB(ctx, u, w, bad); e == nil {
		t.Fatal("bad timezone")
	}
	bad = in
	bad.LocalTime = "25:00"
	if _, e = manager.ScheduleWB(ctx, u, w, bad); e == nil {
		t.Fatal("bad time")
	}
	negative := -1
	bad = in
	bad.LowStockThreshold = &negative
	if _, e = manager.ScheduleWB(ctx, u, w, bad); e == nil {
		t.Fatal("bad threshold")
	}
	// Even a valid metadata grant cannot inject tool or credential parameters.
	manager.mu.Lock()
	manager.grants["typed-input-test"] = scope{u, w, time.Now().Add(time.Minute), ScheduleTool}
	manager.mu.Unlock()
	if e = manager.Client.Schedule(ctx, "typed-input-test", map[string]any{"local_time": "08:00", "timezone": "Europe/Moscow", "notification_enabled": false, "workshop_id": w, "token": "synthetic-secret"}, &output); e == nil {
		t.Fatal("unknown schema fields")
	}
	output, e = manager.ScheduleWB(ctx, u, w, in)
	if e != nil || output.JobID == 0 || output.Schedule.Type != "DAILY_AT_TIME" {
		t.Fatal(output, e)
	}
	again, e := manager.ScheduleWB(ctx, u, w, in)
	if e != nil || again.JobID != output.JobID {
		t.Fatal("non-idempotent creation", again, e)
	}
	if api.prices.Load() != 0 || api.stocks.Load() != 0 {
		t.Fatal("scheduling tool executed work")
	}
	var runs int
	if e = ws.DB().QueryRow(`SELECT COUNT(*) FROM background_job_runs`).Scan(&runs); e != nil || runs != 0 {
		t.Fatal(runs, e)
	}
	j, e := jobs.Get(u, w)
	if e != nil {
		t.Fatal(e)
	}
	data, _ := json.Marshal(j)
	if strings.Contains(string(data), "synthetic-secret") {
		t.Fatal("secret persisted")
	}
	if e = jobs.RunNow(ctx, u, w); e != nil {
		t.Fatal(e)
	}
	if api.prices.Load() != 1 || api.stocks.Load() != 1 {
		t.Fatal("executor bypassed MCP tools")
	}
	if e = jobs.Tick(ctx); e != nil {
		t.Fatal(e)
	}
	if api.prices.Load() != 1 {
		t.Fatal("duplicate execution")
	}
	other, e := ws.CreateOwnedWorkshop(u, "Other")
	if e != nil {
		t.Fatal(e)
	}
	if e = ws.SetActiveWorkshop(u, other); e != nil {
		t.Fatal(e)
	}
	if _, e = manager.ScheduleWB(ctx, u, w, in); e == nil {
		t.Fatal("stale workshop accepted")
	}
	if e = ws.SetActiveWorkshop(u, w); e != nil {
		t.Fatal(e)
	}
	manager.mu.Lock()
	manager.grants["one-use"] = scope{u, w, time.Now().Add(time.Minute), ScheduleTool}
	manager.mu.Unlock()
	if e = manager.Client.Schedule(ctx, "one-use", in, &output); e != nil {
		t.Fatal(e)
	}
	if e = manager.Client.Schedule(ctx, "one-use", in, &output); e == nil {
		t.Fatal("grant replay accepted")
	}
	// Revoked management permission is checked again by the same service.
	if _, e = ws.DB().Exec(`UPDATE workshop_members SET role='VIEWER' WHERE user_id=? AND workshop_id=?`, u, w); e != nil {
		t.Fatal(e)
	}
	if _, e = manager.ScheduleWB(ctx, u, w, in); e == nil {
		t.Fatal("read role scheduled mutation")
	}
	if e = manager.Close(); e != nil {
		t.Fatal(e)
	}
	if state = manager.Client.State(); !state.SessionClosed || !state.ServerClosed {
		t.Fatal(state)
	}
}
