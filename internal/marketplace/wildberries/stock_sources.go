package wildberries

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

type StockSource string

const (
	StockInternal StockSource = "INTERNAL"
	StockSeller   StockSource = "SELLER"
	StockWB       StockSource = "WB"
)

type StockRejection struct {
	Index      int    `json:"index"`
	ExternalID int64  `json:"external_id"`
	Field      string `json:"field"`
	Expected   string `json:"expected"`
	Actual     string `json:"actual"`
}
type StockInfo struct {
	Caller             string      `json:"caller"`
	Tool               string      `json:"mcp_tool"`
	WBMethod           string      `json:"wb_method"`
	RateKey            string      `json:"rate_key"`
	HTTPMethod         string      `json:"http_method"`
	Source             StockSource `json:"source"`
	Status             string      `json:"status"`
	Received           int         `json:"received_records"`
	Valid              int         `json:"valid_records"`
	Invalid            int         `json:"invalid_records"`
	Missing            int         `json:"missing_records"`
	Saved              int         `json:"saved_records"`
	Error              string      `json:"error_category"`
	Endpoint           string      `json:"endpoint"`
	HTTPStatus         int         `json:"http_status"`
	RetryAt            string      `json:"retry_not_before"`
	CapturedAt         string      `json:"captured_at"`
	StartedAt          string      `json:"started_at"`
	DurationMS         int64       `json:"duration_ms"`
	TokenType          string      `json:"token_type"`
	RequiredCapability string      `json:"required_capability"`
}
type StockBatch struct {
	Info       StockInfo        `json:"info"`
	Rows       []Stock          `json:"rows"`
	Rejections []StockRejection `json:"rejections"`
}

func (b StockBatch) Usable() bool {
	return b.Info.Status == "SUCCESS" || b.Info.Status == "PARTIAL" && len(b.Rows) > 0
}

type StockPartialError struct {
	Batch StockBatch
	Cause error
}

func (e *StockPartialError) Error() string { return "stock_partial" }
func (e *StockPartialError) Unwrap() error {
	if e.Cause != nil {
		return e.Cause
	}
	return InvalidResponse
}
func NewStockBatch(src StockSource, rows []Stock, err error) StockBatch {
	var partial *StockPartialError
	if errors.As(err, &partial) {
		return partial.Batch
	}
	if rows == nil {
		rows = []Stock{}
	}
	b := StockBatch{Rows: rows, Rejections: []StockRejection{}, Info: StockInfo{Source: src, Status: "SUCCESS", Received: len(rows), Valid: len(rows), CapturedAt: time.Now().UTC().Format(time.RFC3339Nano)}}
	b.Info.StartedAt = b.Info.CapturedAt
	b.Info.HTTPMethod = "POST"
	b.Info.RateKey = "marketplace"
	b.Info.WBMethod = "SellerStocks"
	b.Info.Tool = "wb_get_seller_stocks"
	b.Info.Endpoint = "/api/v3/stocks/{warehouseId}"
	b.Info.RequiredCapability = "Marketplace"
	if src == StockWB {
		b.Info.Endpoint = "/api/analytics/v1/stocks-report/wb-warehouses"
		b.Info.RequiredCapability = "PERSONAL or SERVICE + Analytics"
		b.Info.RateKey = "analytics"
		b.Info.WBMethod = "WBStocks"
		b.Info.Tool = "wb_get_wb_stocks"
	}
	if err != nil {
		b.Info.Status = "FAILED"
		b.Info.Error = "source_error"
		switch {
		case errors.Is(err, Forbidden) || err.Error() == "wb_access_denied":
			b.Info.Status = "PERMISSION_DENIED"
			b.Info.HTTPStatus = 403
			b.Info.Error = "forbidden"
		case errors.Is(err, Unauthorized) || err.Error() == "wb_authentication_error":
			b.Info.Error = "authentication_failed"
			b.Info.HTTPStatus = 401
		case errors.Is(err, RateLimited) || err.Error() == "wb_rate_limit":
			b.Info.Status = "RATE_LIMITED"
			b.Info.Error = "rate_limited"
		case errors.Is(err, InvalidResponse):
			b.Info.Error = "invalid_response"
		}
		var rate *RateLimitError
		if errors.As(err, &rate) {
			if !rate.RetryAt.IsZero() {
				b.Info.RetryAt = rate.RetryAt.UTC().Format(time.RFC3339Nano)
			}
			if !rate.BlockedLocally {
				b.Info.HTTPStatus = 429
			}
		}
		if len(rows) > 0 {
			b.Info.Status = "PARTIAL"
		}
	} else {
		b.Info.HTTPStatus = 200
	}
	return b
}
func (b *StockBatch) reject(index int, id int64, field, expected, actual string) {
	b.Info.Invalid++
	if len(b.Rejections) < 50 {
		b.Rejections = append(b.Rejections, StockRejection{index, id, field, expected, actual})
	}
}
func (c *Client) SellerStockBatch(ctx context.Context, cards []Card) (out StockBatch) {
	start := time.Now()
	out = NewStockBatch(StockSeller, []Stock{}, nil)
	out.Info.TokenType = c.TokenType()
	out.Info.Caller = TraceMetadata(ctx).Caller
	out.Info.StartedAt = start.UTC().Format(time.RFC3339Nano)
	defer func() {
		out.Info.CapturedAt = time.Now().UTC().Format(time.RFC3339Nano)
		out.Info.DurationMS = time.Since(start).Milliseconds()
		out.Info.Valid = len(out.Rows)
		if out.Info.Invalid > 0 || out.Info.Missing > 0 {
			if out.Info.Error == "" {
				out.Info.Error = "invalid_response"
			}
			out.Info.Status = "PARTIAL"
		}
		if !out.Usable() && len(out.Rows) > 0 {
			out.Info.Status = "PARTIAL"
		}
	}()
	fail := func(e error) {
		info := NewStockBatch(StockSeller, nil, e).Info
		out.Info.Status = info.Status
		out.Info.Error = info.Error
		out.Info.HTTPStatus = info.HTTPStatus
		out.Info.RetryAt = info.RetryAt
	}
	var warehouses []struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	if e := c.request(ctx, "GET", "marketplace-api.wildberries.ru", "/api/v3/warehouses", nil, &warehouses); e != nil {
		fail(e)
		return
	}
	if warehouses == nil || len(warehouses) > 1000 {
		out.reject(-1, 0, "warehouses", "array <=1000", "schema_mismatch")
		return
	}
	ids := []int64{}
	nm := map[int64]int64{}
	for _, card := range cards {
		for _, v := range card.Sizes {
			if v.ID <= 0 || card.ID <= 0 {
				fail(InvalidInput)
				return
			}
			if _, ok := nm[v.ID]; !ok {
				ids = append(ids, v.ID)
			}
			nm[v.ID] = card.ID
		}
	}
	if len(ids) > maxRows {
		fail(PageLimit)
		return
	}
	for _, w := range warehouses {
		if w.ID <= 0 || len(w.Name) > 500 {
			out.reject(-1, w.ID, "warehouse", "positive ID and bounded name", "invalid")
			continue
		}
		for start := 0; start < len(ids); start += 1000 {
			chunk := ids[start:min(start+1000, len(ids))]
			expected := map[int64]bool{}
			for _, id := range chunk {
				expected[id] = true
			}
			var response struct {
				Stocks []json.RawMessage `json:"stocks"`
			}
			if e := c.request(ctx, "POST", "marketplace-api.wildberries.ru", fmt.Sprintf("/api/v3/stocks/%d", w.ID), map[string]any{"chrtIds": chunk}, &response); e != nil {
				fail(e)
				return
			}
			if response.Stocks == nil {
				out.reject(-1, w.ID, "stocks", "array", "missing_or_null")
				continue
			}
			seen := map[int64]bool{}
			for i, raw := range response.Stocks {
				out.Info.Received++
				var fields map[string]json.RawMessage
				var row struct {
					ID     int64
					Amount *int64
				}
				if json.Unmarshal(raw, &fields) != nil || fields == nil {
					out.reject(i, 0, "record", "object", stockJSONType(raw))
					continue
				}
				if json.Unmarshal(fields["chrtId"], &row.ID) != nil || row.ID <= 0 {
					out.reject(i, 0, "chrtId", "positive integer", stockJSONType(fields["chrtId"]))
					continue
				}
				if json.Unmarshal(fields["amount"], &row.Amount) != nil {
					out.reject(i, row.ID, "amount", "nonnegative integer", stockJSONType(fields["amount"]))
					seen[row.ID] = true
					continue
				}

				if !expected[row.ID] || seen[row.ID] {
					out.reject(i, row.ID, "chrtId", "unique requested ID", "unknown_or_duplicate")
					continue
				}
				seen[row.ID] = true
				if row.Amount == nil || *row.Amount < 0 {
					out.reject(i, row.ID, "amount", "nonnegative integer", "missing_or_negative")
					continue
				}
				out.Rows = append(out.Rows, Stock{NmID: nm[row.ID], ChrtID: row.ID, WarehouseID: w.ID, WarehouseName: w.Name, Quantity: *row.Amount})
				if len(out.Rows) > maxRows {
					out.Rows = out.Rows[:maxRows]
					fail(PageLimit)
					return
				}
			}
			for id := range expected {
				if !seen[id] {
					out.Info.Missing++
					if len(out.Rejections) < 50 {
						out.Rejections = append(out.Rejections, StockRejection{-1, id, "chrtId", "requested record", "absent_not_zero"})
					}
				}
			}
		}
	}
	return
}

func stockJSONType(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "missing"
	}
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return "invalid_json"
	}
	switch v.(type) {
	case nil:
		return "null"
	case string:
		return "string"
	case float64:
		return "number"
	case bool:
		return "boolean"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	}
	return "unknown"
}
