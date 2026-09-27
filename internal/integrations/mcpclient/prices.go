package mcpclient

import (
	"context"
	"time"
	wb "workshop-agent/internal/marketplace/wildberries"
)

const PricesTool = "wb_get_prices"

// DiagnosticPrices probes the registered read tool without an identity refresh.
// Only the local operator CLI uses this; it does not import or return seller data.
// The caller must apply the client's one-request budget before connecting.
func (s *Service) DiagnosticPrices(ctx context.Context, workshop int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.connect(ctx); err != nil {
		return err
	}
	ctx = wb.WithTrace(ctx, wb.CallMetadata{WorkshopID: workshop, Caller: "diagnostic"})
	var discarded Envelope[[]wb.Price]
	return s.callTool(ctx, PricesTool, NoArgs{}, &discarded)
}

// Prices reuses the same SDK session, dispatcher, identity and error boundary.
func (s *Service) Prices(ctx context.Context, sellerID string) (out Envelope[[]wb.Price], err error) {
	if sellerID == "" {
		return out, Error("wb_identity_required")
	}
	if !s.mu.TryLock() {
		return out, Error("mcp_busy")
	}
	defer s.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 100*time.Second)
	defer cancel()
	if err = s.connect(ctx); err != nil {
		return out, err
	}
	found := false
	for _, tool := range s.discovery.Tools {
		if tool.Name == PricesTool && tool.ReadOnly {
			found = true
		}
	}
	if !found {
		return out, Error("mcp_tool_policy_error")
	}
	m := wb.TraceMetadata(ctx)
	m.ExpectedSeller = sellerID
	ctx = wb.WithTrace(ctx, m)
	if err = s.callTool(ctx, PricesTool, NoArgs{}, &out); err != nil {
		return out, err
	}
	if !out.Complete || !out.UntrustedData || out.Source != "wildberries" || out.Data == nil {
		return out, Error("mcp_invalid_result")
	}
	if _, e := time.Parse(time.RFC3339, out.FetchedAt); e != nil {
		return out, Error("mcp_invalid_result")
	}
	return out, nil
}
