package mcpmanager

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"time"
	"workshop-agent/internal/background"
)

type pipelineGrant struct {
	Context background.PipelineContext
	Tool    string
	Expires time.Time
}

func (m *Manager) consumePipeline(req *mcp.CallToolRequest, name string) (background.PipelineContext, error) {
	key, _ := req.Params.Meta["pipeline-grant"].(string)
	m.mu.Lock()
	g, ok := m.pipelineGrants[key]
	delete(m.pipelineGrants, key)
	m.mu.Unlock()
	if !ok || g.Tool != name || time.Now().After(g.Expires) {
		return background.PipelineContext{}, errors.New("pipeline_denied")
	}
	return g.Context, nil
}
func (m *Manager) registerPipeline(server *mcp.Server, jobs *background.Service) {
	mcp.AddTool(server, &mcp.Tool{Name: background.BuildTool, Description: "Compute market aggregate and diffs from typed prices and stocks; local SQLite reads only; WB_WRITE=false.", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}}, func(ctx context.Context, req *mcp.CallToolRequest, in background.MarketInput) (*mcp.CallToolResult, background.Aggregate, error) {
		p, e := m.consumePipeline(req, background.BuildTool)
		if e != nil || jobs == nil {
			return nil, background.Aggregate{}, errors.New("pipeline_denied")
		}
		out, e := jobs.WB.BuildMarketSummary(ctx, p, in)
		if e != nil {
			return nil, background.Aggregate{}, errors.New("pipeline_compute_failed")
		}
		return nil, out, nil
	})
	mcp.AddTool(server, &mcp.Tool{Name: background.SaveTool, Description: "Atomically save prices, stocks and aggregate to existing local snapshots. LOCAL_WRITE; WB_WRITE=false.", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false}}, func(ctx context.Context, req *mcp.CallToolRequest, in background.SaveInput) (*mcp.CallToolResult, background.SaveResult, error) {
		p, e := m.consumePipeline(req, background.SaveTool)
		if e != nil || jobs == nil {
			return nil, background.SaveResult{}, errors.New("pipeline_denied")
		}
		out, e := jobs.WB.SaveMarketSnapshot(ctx, p, in)
		if e != nil {
			return nil, background.SaveResult{}, errors.New("pipeline_save_failed")
		}
		return nil, out, nil
	})
}
func (m *Manager) CallPipelineTool(ctx context.Context, p background.PipelineContext, name string, in, out any) error {
	if name != background.BuildTool && name != background.SaveTool {
		return errors.New("pipeline_tool_denied")
	}
	var b [32]byte
	if _, e := rand.Read(b[:]); e != nil {
		return errors.New("pipeline_unavailable")
	}
	key := hex.EncodeToString(b[:])
	m.mu.Lock()
	m.pipelineGrants[key] = pipelineGrant{p, name, time.Now().Add(time.Minute)}
	m.mu.Unlock()
	defer func() { m.mu.Lock(); delete(m.pipelineGrants, key); m.mu.Unlock() }()
	return m.Client.LocalPipeline(ctx, name, key, in, out)
}
