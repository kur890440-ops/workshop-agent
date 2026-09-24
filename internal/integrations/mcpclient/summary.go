package mcpclient

import (
	"context"
	"encoding/json"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// DailySummary only invokes the stored-summary read tool. It does not perform
// WB identity checks or dispatch any of the live WB read tools.
func (s *Service) DailySummary(ctx context.Context, grant string, out any) error {
	if !s.mu.TryLock() {
		return Error("mcp_busy")
	}
	defer s.mu.Unlock()
	if e := s.connect(ctx); e != nil {
		return e
	}
	if !s.readOnly("wb_get_daily_summary") {
		return Error("mcp_tool_policy_error")
	}
	result, e := s.session.CallTool(ctx, &mcp.CallToolParams{Name: "wb_get_daily_summary", Arguments: NoArgs{}, Meta: mcp.Meta{"workshop-grant": grant}})
	if e != nil || result.IsError {
		return Error("summary_denied_or_unavailable")
	}
	raw, e := json.Marshal(result.StructuredContent)
	if e != nil || len(raw) > 65536 || string(raw) == "null" || json.Unmarshal(raw, out) != nil {
		return Error("mcp_invalid_result")
	}
	return nil
}
