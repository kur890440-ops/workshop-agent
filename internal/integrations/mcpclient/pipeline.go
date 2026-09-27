package mcpclient

import (
	"context"
	"encoding/json"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// LocalPipeline dispatches only the two fixed local tools. Authority is an
// expiring one-use grant minted by the application manager, not JSON arguments.
func (s *Service) LocalPipeline(ctx context.Context, name, grant string, in, out any) error {
	if name != "wb_build_market_summary" && name != "wb_save_market_snapshot" {
		return Error("mcp_tool_not_allowed")
	}
	if !s.mu.TryLock() {
		return Error("mcp_busy")
	}
	defer s.mu.Unlock()
	if e := s.connect(ctx); e != nil {
		return e
	}
	raw, e := json.Marshal(in)
	if e != nil || len(raw) > 3<<20 {
		return Error("mcp_invalid_result")
	}
	result, e := s.session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: json.RawMessage(raw), Meta: mcp.Meta{"pipeline-grant": grant}})
	if e != nil || result == nil || result.IsError {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return Error("pipeline_local_step_failed")
	}
	raw, e = json.Marshal(result.StructuredContent)
	if e != nil || len(raw) > 65536 || string(raw) == "null" || json.Unmarshal(raw, out) != nil {
		return Error("mcp_invalid_result")
	}
	return nil
}
