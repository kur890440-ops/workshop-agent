package ozon

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

const maxPages = 100

// Fetch normalizes only the minimal reference-backed schema described in docs.
// Envelope/row validation and finite pagination are separate: bad rows do not
// erase good rows, but a missing collection or pagination flag is never success.
func (c *Client) Fetch(ctx context.Context, source string, skus []int64) Result {
	r := newResult(source)
	counter := &requestCounter{}
	ctx = context.WithValue(ctx, counterKey{}, counter)
	switch source {
	case CatalogSource:
		c.catalog(ctx, &r)
	case SellerSource:
		c.seller(ctx, &r, skus)
	case FBOSource:
		c.fbo(ctx, &r, skus)
	default:
		r.fail(&Error{Code: InvalidInput})
	}
	r.Metrics.HTTPRequests = int(counter.n.Load())
	r.Metrics.Valid = len(r.Products) + len(r.Stocks)
	if r.Status == Partial && r.ErrorCode == "" {
		r.ErrorCode = string(InvalidResponse)
	}
	if r.Status == Partial && r.Metrics.Valid == 0 {
		r.Status = string(InvalidResponse)
	}
	return r
}
func pageContext(ctx context.Context, n int) context.Context {
	m, _ := ctx.Value(contextKey{}).(Metadata)
	m.Page = n
	return WithMetadata(ctx, m)
}

func sentCount(ctx context.Context) int64 {
	if c, ok := ctx.Value(counterKey{}).(*requestCounter); ok {
		return c.n.Load()
	}
	return 0
}
func (c *Client) catalog(ctx context.Context, r *Result) {
	cursor := ""
	seenCursor := map[string]bool{}
	seen := map[int64]bool{}
	complete := false
	for p := 1; p <= maxPages; p++ {
		var req ProductsRequest
		req.Filter.Visibility = "ALL"
		req.Limit = 100
		req.LastID = cursor
		before := sentCount(ctx)
		raw, err := c.ProductsPage(pageContext(ctx, p), req)
		if sentCount(ctx) > before {
			r.Metrics.Pages++
		}
		if err != nil {
			r.fail(err)
			break
		}
		var page catalogPage
		if json.Unmarshal(raw, &page) != nil || page.Result == nil || page.Result.Items == nil {
			r.reject(products, 0, "result", "items array + total", "missing_or_invalid")
			break
		}
		for i, b := range page.Result.Items {
			r.Metrics.Received++
			var v listItem
			if json.Unmarshal(b, &v) != nil || v.ID <= 0 || v.Offer == "" || !textOK(v.Offer, 512) || seen[int64(v.ID)] {
				r.reject(products, i, "product_id/offer_id", "unique positive ID and offer", "invalid_or_duplicate")
				continue
			}
			seen[int64(v.ID)] = true
			r.Products = append(r.Products, Product{ProductID: int64(v.ID), OfferID: clean(v.Offer), SKUs: []int64{}, LastSeenAt: r.CapturedAt})
		}
		if page.Result.Total == nil || *page.Result.Total < 0 {
			r.reject(products, 0, "total", "nonnegative total", "missing_or_invalid")
			break
		}
		if r.Metrics.Received >= *page.Result.Total {
			complete = true
			break
		}
		cursor = page.Result.LastID
		if cursor == "" || len(page.Result.Items) == 0 || seenCursor[cursor] {
			r.reject(products, 0, "last_id", "advancing cursor until total", "incomplete_or_repeated")
			break
		}
		seenCursor[cursor] = true
	}
	if !complete && r.Status == Success {
		r.reject(products, 0, "pagination", "bounded complete catalog", "page_limit")
	}
	// Details are fetched only during explicit catalog refresh, in batches of100.
	for start := 0; start < len(r.Products); start += 100 {
		end := start + 100
		if end > len(r.Products) {
			end = len(r.Products)
		}
		ids := make([]int64, 0, end-start)
		indices := map[int64]int{}
		for i := start; i < end; i++ {
			ids = append(ids, r.Products[i].ProductID)
			indices[r.Products[i].ProductID] = i
		}
		raw, e := c.ProductDetailsBatch(pageContext(ctx, 0), DetailsRequest{ProductIDs: ids})
		if e != nil {
			r.fail(e)
			return
		}
		var page detailsPage
		if json.Unmarshal(raw, &page) != nil || page.Items == nil {
			r.reject(details, 0, "items", "array", "missing_or_invalid")
			continue
		}
		got := map[int64]bool{}
		for i, b := range page.Items {
			var v detailItem
			e := json.Unmarshal(b, &v)
			idx, ok := indices[int64(v.ID)]
			if e != nil || !ok || got[int64(v.ID)] || v.Offer != r.Products[idx].OfferID || !textOK(v.Name, 512) || len(v.Sources) > 100 {
				r.reject(details, i, "id/offer_id", "requested unique product", "invalid_or_unexpected")
				continue
			}
			if v.Updated != "" {
				if _, e := time.Parse(time.RFC3339Nano, v.Updated); e != nil {
					r.reject(details, i, "updated_at", "RFC3339", "invalid")
					continue
				}
			}
			values := []int64{}
			dedup := map[int64]bool{}
			if v.SKU > 0 {
				values = append(values, int64(v.SKU))
				dedup[int64(v.SKU)] = true
			}
			for _, s := range v.Sources {
				if s.SKU > 0 && !dedup[int64(s.SKU)] {
					values = append(values, int64(s.SKU))
					dedup[int64(s.SKU)] = true
				}
			}
			got[int64(v.ID)] = true
			r.Products[idx].Name = clean(v.Name)
			r.Products[idx].SKUs = values
			r.Products[idx].UpdatedAt = v.Updated
		}
		if len(got) != len(ids) {
			r.reject(details, 0, "items", "all requested identifiers", "missing_details")
		}
	}
}
func (c *Client) seller(ctx context.Context, r *Result, skus []int64) {
	ids := []string{}
	unique := map[int64]bool{}
	for _, sku := range skus {
		if sku <= 0 {
			r.Metrics.IdentifiersInvalid++
			continue
		}
		if unique[sku] {
			r.Metrics.IdentifiersDuplicate++
			continue
		}
		unique[sku] = true
		ids = append(ids, fmt.Sprint(sku))
	}
	r.Metrics.IdentifiersValid = len(ids)
	r.Metrics.CatalogSource = "CACHE HIT"
	if len(ids) == 0 {
		r.Status = "UNAVAILABLE"
		r.ErrorCode = "CATALOG_SKUS_REQUIRED"
		r.Metrics.CatalogSource = "CACHE MISS"
		return
	}
	r.Metrics.Batches = (len(ids) + 99) / 100
	keys := map[string]bool{}
	for start := 0; start < len(ids); start += 100 {
		end := start + 100
		if end > len(ids) {
			end = len(ids)
		}
		m := TraceMetadata(ctx)
		m.Batch = start/100 + 1
		m.Batches = r.Metrics.Batches
		m.IdentifierCount = end - start
		if !c.sellerBatch(WithMetadata(ctx, m), r, ids[start:end], keys) {
			return
		}
	}
}
func (c *Client) sellerBatch(ctx context.Context, r *Result, ids []string, keys map[string]bool) bool {
	cursor := ""
	seen := map[string]bool{}
	allowed := map[string]bool{}
	for _, id := range ids {
		allowed[id] = true
	}
	for p := 1; p <= maxPages; p++ {
		before := sentCount(ctx)
		raw, e := c.SellerStocksPage(pageContext(ctx, p), SellerStocksRequest{SKUs: ids, Limit: 100, Cursor: cursor})
		if sentCount(ctx) > before {
			r.Metrics.Pages++
		}
		if e != nil {
			r.fail(e)
			return false
		}
		var page sellerPage
		if json.Unmarshal(raw, &page) != nil || page.Products == nil {
			r.reject(seller, 0, "products/has_next", "array and boolean", "missing_or_invalid")
			return false
		}
		filtered := []json.RawMessage{}
		for i, b := range page.Products {
			var v struct {
				SKU integer `json:"sku"`
			}
			if json.Unmarshal(b, &v) != nil || !allowed[fmt.Sprint(v.SKU)] {
				r.Metrics.Received++
				r.reject(seller, i, "sku", "requested SKU", "invalid_or_unrequested")
				continue
			}
			filtered = append(filtered, b)
		}
		appendStocks(r, seller, filtered, keys)
		if page.HasNext == nil {
			r.reject(seller, 0, "has_next", "boolean", "missing_or_invalid")
			return false
		}
		if !*page.HasNext {
			return true
		}
		cursor = page.Cursor
		if cursor == "" || seen[cursor] || len(page.Products) == 0 {
			r.reject(seller, 0, "cursor", "advancing cursor", "incomplete_or_repeated")
			return false
		}
		seen[cursor] = true
	}
	r.reject(seller, 0, "pagination", "at most100 pages", "page_limit")
	return false
}
func (c *Client) fbo(ctx context.Context, r *Result, skus []int64) {
	if len(skus) == 0 {
		r.Status = "UNAVAILABLE"
		r.ErrorCode = "CATALOG_SKUS_REQUIRED"
		return
	}
	keys := map[string]bool{}
	for start := 0; start < len(skus); start += 100 {
		end := start + 100
		if end > len(skus) {
			end = len(skus)
		}
		ids := []string{}
		allowed := map[int64]bool{}
		for _, id := range skus[start:end] {
			ids = append(ids, fmt.Sprint(id))
			allowed[id] = true
		}
		before := sentCount(ctx)
		raw, e := c.AnalyticsStocksBatch(pageContext(ctx, r.Metrics.Pages+1), AnalyticsRequest{SKUs: ids})
		if sentCount(ctx) > before {
			r.Metrics.Pages++
		}
		if e != nil {
			r.fail(e)
			return
		}
		var page detailsPage
		if json.Unmarshal(raw, &page) != nil || page.Items == nil {
			r.reject(analytics, 0, "items", "array", "missing_or_invalid")
			continue
		}
		filtered := []json.RawMessage{}
		for i, b := range page.Items {
			var v stockItem
			if json.Unmarshal(b, &v) != nil || !allowed[int64(v.SKU)] {
				r.Metrics.Received++
				r.reject(analytics, i, "sku", "requested SKU", "invalid_or_unexpected")
				continue
			}
			filtered = append(filtered, b)
		}
		appendStocks(r, analytics, filtered, keys)
	}
	// Completeness applies to requested cached SKUs only, never the entire account.
}
func appendStocks(r *Result, op operation, items []json.RawMessage, keys map[string]bool) {
	for i, b := range items {
		r.Metrics.Received++
		v, ok := stockRow(b, r.Source, r.CapturedAt)
		key := fmt.Sprintf("%d/%d", v.SKU, v.WarehouseID)
		if !ok || keys[key] {
			r.reject(op, i, "sku/warehouse/quantity", "unique IDs and explicit nonnegative quantity", "invalid_missing_or_duplicate")
			continue
		}
		keys[key] = true
		r.Stocks = append(r.Stocks, v)
	}
}
