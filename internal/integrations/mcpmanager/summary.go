package mcpmanager

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"time"
	"workshop-agent/internal/background"
	"workshop-agent/internal/integrations/mcpclient"
)

const SummaryTool = "wb_get_daily_summary"

func (m *Manager) registerSummary(server *mcp.Server, jobs *background.Service) {
	no := false
	mcp.AddTool(server, &mcp.Tool{Name: SummaryTool, Description: "Возвращает сохранённую последнюю успешную или частичную утреннюю сводку WB активной мастерской. Только чтение SQLite; без запросов WB.", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, DestructiveHint: &no}},
		func(ctx context.Context, req *mcp.CallToolRequest, _ mcpclient.NoArgs) (*mcp.CallToolResult, background.DailySummary, error) {
			key, _ := req.Params.Meta["workshop-grant"].(string)
			m.mu.Lock()
			sc, ok := m.grants[key]
			delete(m.grants, key)
			m.mu.Unlock()
			if !ok || sc.tool != SummaryTool || time.Now().After(sc.expires) || jobs == nil {
				return nil, background.DailySummary{}, errors.New("summary_denied_or_unavailable")
			}
			out, e := jobs.DailySummary(sc.user, sc.workshop)
			if e != nil {
				return nil, background.DailySummary{}, errors.New("summary_denied_or_unavailable")
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: out.Summary}}}, out, nil
		})
}

func (m *Manager) DailySummary(ctx context.Context, user, workshop int64) (out background.DailySummary, err error) {
	var random [32]byte
	if _, err = rand.Read(random[:]); err != nil {
		return out, errors.New("summary_unavailable")
	}
	key := hex.EncodeToString(random[:])
	m.mu.Lock()
	m.grants[key] = scope{user, workshop, time.Now().Add(time.Minute), SummaryTool}
	m.mu.Unlock()
	defer func() { m.mu.Lock(); delete(m.grants, key); m.mu.Unlock() }()
	err = m.Client.DailySummary(ctx, key, &out)
	return
}
