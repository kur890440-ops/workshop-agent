package mcpmanager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
	"workshop-agent/internal/auth"
	"workshop-agent/internal/marketplace"
	"workshop-agent/internal/marketplace/ozon"
	wb "workshop-agent/internal/marketplace/wildberries"
	q "workshop-agent/internal/marketplacequery"
)

// QueryAdapter reuses the manager's single client/session and existing access gates.
type QueryAdapter struct {
	Manager *Manager
	WB      *marketplace.Service
	Ozon    *ozon.Service
}

func (a *QueryAdapter) QueryTools() []q.Tool {
	out := q.Registry()
	found := a.Manager.Client.State()
	for i := range out {
		for _, t := range found.Tools {
			if t.Name == out[i].Name {
				out[i].Available = t.ReadOnly
			}
		}
	}
	return out
}
func (a *QueryAdapter) QueryCall(ctx context.Context, sc q.Scope, t q.Tool) (q.SourceResult, error) {
	out := q.SourceResult{Status: "UNAVAILABLE", Rows: []q.Row{}}
	allowed := false
	for _, v := range a.QueryTools() {
		if v == t && v.Available && v.Classification == "READ_ONLY" {
			allowed = true
		}
	}
	if !allowed {
		return out, fmt.Errorf("query tool denied")
	}
	if t.Provider == q.Ozon {
		if a.Ozon == nil {
			return out, nil
		}
		access := ozon.Access{UserID: sc.UserID, WorkshopID: sc.WorkshopID, ConnectionID: 2}
		c, e := a.Ozon.Status(access)
		if e != nil || !c.Enabled {
			return out, nil
		}
		out.ConnectionID = c.ID
		runID := int64(0)
		for offset := 0; offset <= 10000; {
			out.MCPCalls++
			v, e := a.Manager.Ozon(ozon.WithMetadata(ctx, ozon.Metadata{Caller: "mcp_tool", Tool: t.Name, Cache: "HIT"}), access, t.Name, ozon.Input{Offset: offset})
			if e != nil {
				return q.SourceResult{Status: "FAILED", ConnectionID: c.ID, MCPCalls: out.MCPCalls}, nil
			}
			if offset > 0 && runID != v.RunID {
				return q.SourceResult{Status: "PARTIAL", ConnectionID: c.ID, MCPCalls: out.MCPCalls}, nil
			}
			runID = v.RunID
			out.Status = v.Status
			out.RetryNotBefore = v.RetryNotBefore
			for _, p := range v.Products {
				out.Rows = append(out.Rows, q.Row{ProductID: p.ProductID, Name: p.Name, CapturedAt: p.LastSeenAt, Confirmed: true})
			}
			for _, s := range v.Stocks {
				out.Rows = append(out.Rows, q.Row{ProductID: s.ProductID, SKU: s.SKU, WarehouseID: s.WarehouseID, Name: s.Name, Quantity: s.Quantity, Confirmed: s.Observation != "MISSING" && s.Observation != "STALE", CapturedAt: s.CapturedAt})
			}
			n := len(v.Products) + len(v.Stocks)
			offset += n
			if offset >= v.Total {
				return out, nil
			}
			if n == 0 {
				break
			}
		}
		out.Status = "PARTIAL"
		return out, nil
	}
	if a.WB == nil {
		return out, nil
	}
	scope := marketplace.Scope{UserID: sc.UserID, WorkshopID: sc.WorkshopID, ConnectionID: 1}
	err := a.WB.MCPRead(ctx, scope, func(ctx context.Context, c marketplace.Connection) error {
		out.ConnectionID = c.ID
		out.MCPCalls++
		v, e := a.Manager.Client.QueryWB(ctx, t.Name, c.SellerID)
		if e != nil {
			var r *wb.RateLimitError
			if errors.As(e, &r) {
				out.Status = "RATE_LIMITED"
				out.RetryNotBefore = r.RetryAt.UTC().Format(time.RFC3339)
			}
			return e
		}
		out.Status = "SUCCESS"
		if !v.Complete {
			out.Status = "PARTIAL"
		}
		if t.Capability == q.Products {
			var cards []wb.Card
			if json.Unmarshal(v.Data, &cards) != nil {
				return fmt.Errorf("invalid result")
			}
			for _, p := range cards {
				out.Rows = append(out.Rows, q.Row{ProductID: p.ID, Name: p.Title, Confirmed: true, CapturedAt: v.FetchedAt})
			}
			return nil
		}
		var rows []wb.Stock
		if t.Source == q.Seller {
			var batch wb.StockBatch
			if json.Unmarshal(v.Data, &batch) != nil {
				return fmt.Errorf("invalid result")
			}
			rows = batch.Rows
			out.Status = batch.Info.Status
			out.RetryNotBefore = batch.Info.RetryAt
		} else {
			if json.Unmarshal(v.Data, &rows) != nil {
				return fmt.Errorf("invalid result")
			}
		}
		for _, r := range rows {
			out.Rows = append(out.Rows, q.Row{ProductID: r.NmID, VariantID: r.ChrtID, Variant: fmt.Sprintf("вариант %d", r.ChrtID), WarehouseID: r.WarehouseID, Quantity: r.Quantity, Confirmed: true, CapturedAt: v.FetchedAt})
		}
		return nil
	})
	if err != nil {
		out.Rows = nil
		if out.Status != "RATE_LIMITED" {
			out.Status = "FAILED"
			if errors.Is(err, auth.ErrDenied) || err.Error() == "wb_access_denied" {
				out.Status = "PERMISSION_DENIED"
			}
		}
	}
	return out, nil
}
