// Package mcpmanager is the application composition root for its one MCP server.
package mcpmanager

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"workshop-agent/internal/background"
	"workshop-agent/internal/integrations/mcpclient"
	"workshop-agent/internal/integrations/wbmcp"
)

const ScheduleTool = "schedule_wb_daily_sync"

type ScheduleInput struct {
	LocalTime           string `json:"local_time" jsonschema:"Local daily time in HH:MM format"`
	Timezone            string `json:"timezone" jsonschema:"Workshop IANA timezone"`
	NotificationEnabled bool   `json:"notification_enabled"`
	LowStockThreshold   *int   `json:"low_stock_threshold,omitempty"`
}
type Schedule struct {
	Type      string `json:"type"`
	LocalTime string `json:"local_time"`
	Timezone  string `json:"timezone"`
}
type ScheduleOutput struct {
	JobID     int64    `json:"job_id"`
	JobType   string   `json:"job_type"`
	Status    string   `json:"status"`
	Schedule  Schedule `json:"schedule"`
	NextRunAt string   `json:"next_run_at"`
}
type scope struct {
	user, workshop int64
	expires        time.Time
	tool           string
}
type Manager struct {
	Client *mcpclient.Service
	mu     sync.Mutex
	grants map[string]scope
}

func New(ctx context.Context, api wbmcp.API, jobs *background.Service) (*Manager, error) {
	server, err := wbmcp.New(api)
	if err != nil {
		return nil, err
	}
	m := &Manager{grants: map[string]scope{}}
	// A single server registers both modules. This local mutation is explicitly
	// separate from WB read tools and never appears in the executor's allowlist.
	mcp.AddTool(server, &mcp.Tool{Name: ScheduleTool, Description: "Создать ежедневную синхронизацию WB в активной мастерской. Локальная запись расписания; WB API не вызывается.", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false}},
		func(ctx context.Context, req *mcp.CallToolRequest, in ScheduleInput) (*mcp.CallToolResult, ScheduleOutput, error) {
			var out ScheduleOutput
			grant, _ := req.Params.Meta["workshop-grant"].(string)
			m.mu.Lock()
			sc, ok := m.grants[grant]
			delete(m.grants, grant)
			m.mu.Unlock()
			if !ok || sc.tool != ScheduleTool || time.Now().After(sc.expires) || jobs == nil {
				return nil, out, errors.New("schedule_denied_or_invalid")
			}
			j, e := jobs.CreateJob(sc.user, sc.workshop, background.CreateInput{Type: background.JobType, LocalTime: in.LocalTime, Timezone: in.Timezone, NotificationEnabled: in.NotificationEnabled, LowStockThreshold: in.LowStockThreshold})
			if e != nil {
				return nil, out, errors.New("schedule_denied_or_invalid")
			}
			out = ScheduleOutput{JobID: j.ID, JobType: j.Type, Status: j.Status, Schedule: Schedule{j.Schedule, j.LocalTime, j.Timezone}, NextRunAt: time.Unix(j.NextRun, 0).UTC().Format(time.RFC3339)}
			return nil, out, nil
		})
	m.registerSummary(server, jobs)
	m.Client, err = mcpclient.NewInMemory(ctx, server)
	if err != nil {
		return nil, err
	}
	return m, nil
}

// ScheduleWB is called only by an authenticated application adapter. Scope is
// carried as a one-use, unguessable metadata grant, never as model tool input.
func (m *Manager) ScheduleWB(ctx context.Context, user, workshop int64, in ScheduleInput) (out ScheduleOutput, err error) {
	var bytes [32]byte
	if _, err = rand.Read(bytes[:]); err != nil {
		return out, errors.New("schedule_unavailable")
	}
	key := hex.EncodeToString(bytes[:])
	m.mu.Lock()
	m.grants[key] = scope{user, workshop, time.Now().Add(time.Minute), ScheduleTool}
	m.mu.Unlock()
	defer func() { m.mu.Lock(); delete(m.grants, key); m.mu.Unlock() }()
	err = m.Client.Schedule(ctx, key, in, &out)
	return
}
func (m *Manager) Close() error { return m.Client.Close() }
