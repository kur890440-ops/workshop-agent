package mcpclient

import (
	"context"
	"time"
	wb "workshop-agent/internal/marketplace/wildberries"
)

const PricesTool = "wb_get_prices"

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
	if s.seller != sellerID || time.Since(s.verified) >= 24*time.Hour {
		var seller Envelope[wb.Seller]
		if err = s.callTool(ctx, "wb_get_seller", NoArgs{}, &seller); err != nil {
			return out, err
		}
		if !seller.Complete || seller.Data.ID != sellerID || seller.Source != "wildberries" {
			return out, Error("wb_identity_mismatch")
		}
		s.seller = sellerID
		s.verified = time.Now()
	}
	if err = s.callTool(ctx, PricesTool, NoArgs{}, &out); err != nil {
		if err == Error("wb_authentication_error") || err == Error("wb_access_denied") {
			s.seller = ""
		}
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
