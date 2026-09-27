package mcpclient

import (
	"context"
	"time"
	wb "workshop-agent/internal/marketplace/wildberries"
)

const SellerStocksTool = "wb_get_seller_stocks"

func (s *Service) StockSource(ctx context.Context, seller string, source wb.StockSource) (wb.StockBatch, error) {
	if source == wb.StockWB {
		start := time.Now()
		v, e := s.Stocks(ctx, seller)
		b := wb.NewStockBatch(source, v.Value.Data, e)
		b.Info.StartedAt = start.UTC().Format(time.RFC3339Nano)
		b.Info.DurationMS = time.Since(start).Milliseconds()
		b.Info.Caller = wb.TraceMetadata(ctx).Caller
		if e == nil {
			b.Info.CapturedAt = v.Value.FetchedAt
		}
		return b, e
	}
	if source != wb.StockSeller || seller == "" {
		return wb.StockBatch{}, Error("wb_identity_required")
	}
	if !s.mu.TryLock() {
		return wb.NewStockBatch(source, nil, Error("mcp_busy")), Error("mcp_busy")
	}
	defer s.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 100*time.Second)
	defer cancel()
	if e := s.connect(ctx); e != nil {
		return wb.NewStockBatch(source, nil, e), e
	}
	meta := wb.TraceMetadata(ctx)
	meta.ExpectedSeller = seller
	ctx = wb.WithTrace(ctx, meta)
	var result Envelope[wb.StockBatch]
	e := s.callTool(ctx, SellerStocksTool, NoArgs{}, &result)
	if e != nil {
		return wb.NewStockBatch(source, nil, e), e
	}
	b := result.Data
	if b.Info.Source != source || len(b.Rows) > 50000 || b.Info.Valid != len(b.Rows) {
		return wb.StockBatch{}, Error("mcp_invalid_result")
	}
	if b.Info.Status != "SUCCESS" {
		var cause error
		if b.Info.Error == "rate_limited" {
			at, _ := time.Parse(time.RFC3339Nano, b.Info.RetryAt)
			cause = &wb.RateLimitError{Operation: "marketplace", RetryAt: at, Source: "wb_retry", BlockedLocally: b.Info.HTTPStatus == 0}
		}
		if b.Info.HTTPStatus == 403 {
			cause = wb.Forbidden
		}
		return b, &wb.StockPartialError{Batch: b, Cause: cause}
	}
	return b, nil
}
