package mcpclient

import (
	"context"
	"encoding/json"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"workshop-agent/internal/marketplace/ozon"
)

// Ozon is a dedicated dispatcher. Ozon tools are not added to LLM selection.
func (s *Service) Ozon(ctx context.Context, name, grant string, in ozon.Input) (ozon.Result, error) {
	var out ozon.Result
	if name != "ozon_list_products" && name != "ozon_get_seller_stocks" && name != "ozon_get_ozon_stocks" {
		return out, Error("mcp_tool_not_allowed")
	}
	s.mu.Lock()
	err := s.connect(ctx)
	session := s.session
	allowed := s.readOnly(name)
	s.mu.Unlock()
	if err != nil {
		return out, err
	}
	if !allowed {
		return out, Error("mcp_tool_not_allowed")
	}
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: in, Meta: mcp.Meta{"ozon-grant": grant}})
	if err != nil || result == nil || result.IsError {
		return out, Error("ozon_operation_denied_or_failed")
	}
	raw, err := json.Marshal(result.StructuredContent)
	if err != nil || len(raw) > 1<<20 || json.Unmarshal(raw, &out) != nil || out.Provider != "OZON" || !out.UntrustedData {
		return ozon.Result{}, Error("mcp_invalid_result")
	}
	return out, nil
}
