package ozon

import (
	"context"
	"encoding/json"
	"time"
)

// These counts describe validation at the HTTP boundary. Cross-page duplicates,
// requested-ID checks and committed rows are reported by the separate SYNC event.
func responseCounts(op operation, raw []byte) (int, int) {
	var items []json.RawMessage
	switch op.name {
	case products.name:
		var p catalogPage
		if json.Unmarshal(raw, &p) == nil && p.Result != nil {
			items = p.Result.Items
		}
	case seller.name:
		var p sellerPage
		if json.Unmarshal(raw, &p) == nil {
			items = p.Products
		}
	default:
		var p detailsPage
		if json.Unmarshal(raw, &p) == nil {
			items = p.Items
		}
	}
	valid := 0
	for _, b := range items {
		ok := false
		switch op.name {
		case products.name:
			var v listItem
			ok = json.Unmarshal(b, &v) == nil && v.ID > 0 && v.Offer != "" && textOK(v.Offer, 512)
		case details.name:
			var v detailItem
			ok = json.Unmarshal(b, &v) == nil && v.ID > 0 && v.Offer != "" && textOK(v.Name, 512)
		case seller.name:
			_, ok = stockRow(b, SellerSource, "")
		case analytics.name:
			_, ok = stockRow(b, FBOSource, "")
		}
		if ok {
			valid++
		}
	}
	return len(items), valid
}
func (s *Service) logicalTrace(ctx context.Context, a Access, r Result, stage string) error {
	m := TraceMetadata(ctx)
	if m.Caller == "" {
		m.Caller = "mcp_tool"
	}
	received, valid, saved := r.Metrics.Received, r.Metrics.Valid, r.Metrics.Saved
	tr := Trace{Stage: stage, Timestamp: time.Now().UTC(), WorkshopID: a.WorkshopID, ConnectionID: a.ConnectionID, Provider: "OZON", Caller: m.Caller, Tool: m.Tool, Operation: r.Source, GoMethod: "Service.Execute", Cache: r.Cache, Dedup: r.Dedup, Result: r.Status, RecordsReceived: &received, RecordsValid: &valid, RecordsSaved: &saved}
	if r.Source == SellerSource {
		metrics := r.Metrics
		if stage == "CACHE" {
			metrics = r.LastMetrics
		}
		tr.RefreshMetrics = &metrics
		tr.Endpoint = seller.path
	}
	if r.RetryNotBefore != "" {
		tr.RetryNotBefore, _ = time.Parse(time.RFC3339Nano, r.RetryNotBefore)
	}
	return (sqliteState{s.db}).AppendTrace(ctx, tr)
}
