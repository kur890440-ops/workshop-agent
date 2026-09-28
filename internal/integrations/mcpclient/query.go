package mcpclient

import (
	"context"
	"encoding/json"
	wb "workshop-agent/internal/marketplace/wildberries"
)

// QueryWB is a closed dispatcher on the existing SDK client. Only the trusted
// application adapter may call it after MCPRead authorizes user/workshop/revision.
func (s *Service) QueryWB(ctx context.Context, name, sellerID string) (Envelope[json.RawMessage], error) {
	var out Envelope[json.RawMessage]
	if sellerID == "" || (name != "wb_get_products" && name != SellerStocksTool && name != StocksTool) {
		return out, Error("mcp_tool_not_allowed")
	}
	if !s.mu.TryLock() {
		return out, Error("mcp_busy")
	}
	defer s.mu.Unlock()
	if e := s.connect(ctx); e != nil {
		return out, e
	}
	meta := wb.TraceMetadata(ctx)
	meta.ExpectedSeller = sellerID
	ctx = wb.WithTrace(ctx, meta)
	e := s.callTool(ctx, name, NoArgs{}, &out)
	if e == nil && (!out.UntrustedData || out.Source != "wildberries") {
		return out, Error("mcp_invalid_result")
	}
	return out, e
}
