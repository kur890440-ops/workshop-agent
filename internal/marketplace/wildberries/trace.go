package wildberries

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type CallMetadata struct {
	WorkshopID     int64  `json:"workshop_id"`
	Caller         string `json:"caller"`
	Tool           string `json:"tool"`
	ExpectedSeller string `json:"expected_seller,omitempty"`
}
type traceKey struct{}

func WithTrace(ctx context.Context, m CallMetadata) context.Context {
	switch m.Caller {
	case "manual_seller_info_refresh", "telegram_wb_sync", "seller_info_refresh", "telegram_wb_stocks", "wb_daily_sync", "run_now", "diagnostic", "mcp_tool":
	default:
		m.Caller = "integration"
	}
	switch m.Tool {
	case "wb_get_seller_stocks", "wb_get_seller", "wb_get_prices", "wb_get_wb_stocks", "wb_get_products", "wb_get_new_orders", "wb_get_order_statuses":
	default:
		m.Tool = ""
	}
	if m.WorkshopID < 0 {
		m.WorkshopID = 0
	}
	return context.WithValue(ctx, traceKey{}, m)
}
func TraceMetadata(ctx context.Context) CallMetadata {
	m, _ := ctx.Value(traceKey{}).(CallMetadata)
	return m
}

type RateObservation struct {
	Group        string    `json:"rate_key"`
	ReceivedAt   time.Time `json:"received_at"`
	Endpoint     string    `json:"endpoint"`
	HTTPStatus   int       `json:"http_status"`
	Remaining    *int64    `json:"remaining"`
	RetrySeconds *int64    `json:"retry_seconds"`
	ResetSeconds *int64    `json:"reset_seconds"`
	Limit        *int64    `json:"limit"`
}
type RequestTrace struct {
	StockSource        StockSource      `json:"stock_source,omitempty"`
	TokenType          string           `json:"token_type,omitempty"`
	RequiredCapability string           `json:"required_capability,omitempty"`
	Host               string           `json:"host"`
	RateKey            string           `json:"rate_key"`
	Operation          string           `json:"operation"`
	RequestStartedAt   time.Time        `json:"request_started_at"`
	RequestFinishedAt  time.Time        `json:"request_finished_at"`
	RequestSent        bool             `json:"request_sent"`
	Detail             string           `json:"detail,omitempty"`
	Limit              *int64           `json:"X-Ratelimit-Limit"`
	Remaining          *int64           `json:"X-Ratelimit-Remaining"`
	Retry              *int64           `json:"X-Ratelimit-Retry"`
	Reset              *int64           `json:"X-Ratelimit-Reset"`
	Timestamp          time.Time        `json:"timestamp"`
	WorkshopID         int64            `json:"workshop_id"`
	Caller             string           `json:"caller"`
	Tool               string           `json:"mcp_tool"`
	Method             string           `json:"http_method"`
	WBMethod           string           `json:"wb_method"`
	Endpoint           string           `json:"endpoint"`
	HTTPStatus         int              `json:"http_status"`
	DurationMS         int64            `json:"duration_ms"`
	Attempt            int              `json:"attempt_number"`
	Result             string           `json:"result"`
	Rate               *RateObservation `json:"rate,omitempty"`
	RetryAt            *time.Time       `json:"retry_not_before"`
}
type TraceStore interface {
	SaveObservation(RateObservation) error
	AppendTrace(RequestTrace) error
}

func numericHeader(h http.Header, key string) *int64 {
	n, e := strconv.ParseInt(strings.TrimSpace(h.Get(key)), 10, 64)
	if e != nil || n < 0 {
		return nil
	}
	return &n
}
func observation(h http.Header, now time.Time, group, path string, status int) RateObservation {
	return RateObservation{group, now, path, status, numericHeader(h, "X-Ratelimit-Remaining"), numericHeader(h, "X-Ratelimit-Retry"), numericHeader(h, "X-Ratelimit-Reset"), numericHeader(h, "X-Ratelimit-Limit")}
}
func pathLabel(path string) string {
	if strings.HasPrefix(path, "/api/v3/stocks/") {
		return "/api/v3/stocks/{warehouse}"
	}
	return path
}
func methodLabel(path string) string {
	switch pathLabel(path) {
	case "/api/v1/seller-info":
		return "Seller"
	case "/api/v2/list/goods/filter":
		return "Prices"
	case "/api/analytics/v1/stocks-report/wb-warehouses":
		return "WBStocks"
	case "/content/v2/get/cards/list":
		return "Catalog"
	case "/api/v3/warehouses", "/api/v3/stocks/{warehouse}":
		return "SellerStocks"
	case "/api/v3/orders/new":
		return "NewOrders"
	case "/api/v3/orders/status":
		return "OrderStatuses"
	}
	return "unknown"
}
func (c *Client) record(ctx context.Context, started time.Time, method, path string, status, attempt int, result string, o *RateObservation, rate *RateLimitError) {
	m := TraceMetadata(ctx)
	if m.Caller == "" {
		m.Caller = "integration"
	}
	finished := c.clock().UTC()
	detail := ""
	sent := status != 0 || result == "NETWORK_ERROR" || result == "TIMEOUT"
	switch result {
	case "CACHED":
		detail, result = result, "SUCCESS"
	case "DIAGNOSTIC_BUDGET":
		detail, result = result, "BLOCKED_LOCALLY"
	case "NETWORK_ERROR", "TIMEOUT", "LOCAL_ERROR":
		detail, result = result, "ERROR"
	}
	number := 0
	if sent {
		number = attempt + 1
	}
	operation := map[string]string{"Seller": "seller_info", "Prices": "prices", "WBStocks": "wb_stocks", "Catalog": "catalog", "SellerStocks": "seller_stocks", "NewOrders": "new_orders", "OrderStatuses": "order_statuses"}[methodLabel(path)]
	caller := m.Caller
	switch caller {
	case "wb_daily_sync":
		caller = "background:WB_DAILY_SYNC"
	case "run_now":
		caller = "background:WB_DAILY_SYNC:run_now"
	case "mcp_tool":
		if m.Tool != "" {
			caller += ":" + m.Tool
		}
	}
	host, group := traceDestination(path)
	r := RequestTrace{Host: host, RateKey: group, Operation: operation, RequestStartedAt: started.UTC(), RequestFinishedAt: finished, RequestSent: sent, Detail: detail, Timestamp: started.UTC(), WorkshopID: m.WorkshopID, Caller: caller, Tool: m.Tool, Method: method, WBMethod: methodLabel(path), Endpoint: path, HTTPStatus: status, DurationMS: finished.Sub(started).Milliseconds(), Attempt: number, Result: result, Rate: o}
	if r.Operation == "seller_stocks" {
		r.StockSource = StockSeller
		r.RequiredCapability = "Marketplace"
	}
	if r.Operation == "wb_stocks" {
		r.StockSource = StockWB
		r.RequiredCapability = "PERSONAL or SERVICE + Analytics"
	}
	if r.StockSource != "" {
		r.TokenType = c.TokenType()
	}
	if o != nil {
		r.Limit = o.Limit
		r.Remaining = o.Remaining
		r.Retry = o.RetrySeconds
		r.Reset = o.ResetSeconds
	}
	if rate != nil {
		at := rate.RetryAt.UTC()
		r.RetryAt = &at
	}
	c.mu.Lock()
	if o != nil {
		if saved := c.cooldowns[o.Group]; saved.RetryAt.After(finished) && (r.RetryAt == nil || saved.RetryAt.After(*r.RetryAt)) {
			at := saved.RetryAt.UTC()
			r.RetryAt = &at
		}
	}
	c.history = append(c.history, r)
	if len(c.history) > 100 {
		c.history = append([]RequestTrace(nil), c.history[len(c.history)-100:]...)
	}
	c.mu.Unlock()
	if s, ok := c.store.(TraceStore); ok {
		_ = s.AppendTrace(r)
	}
}
func (c *Client) recordCache(ctx context.Context) {
	c.record(ctx, c.clock(), "GET", "/api/v1/seller-info", 0, 0, "CACHED", nil, nil)
}
func (c *Client) History() []RequestTrace {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]RequestTrace(nil), c.history...)
}
func (c *Client) observe(o RateObservation) {
	if s, ok := c.store.(TraceStore); ok {
		_ = s.SaveObservation(o)
	}
}

// All paths reaching the recorder have passed endpoint() or are the fixed cache path.
func traceDestination(path string) (host, group string) {
	switch methodLabel(path) {
	case "Seller":
		return "common-api.wildberries.ru", "common"
	case "Prices":
		return "discounts-prices-api.wildberries.ru", "prices"
	case "WBStocks":
		return "seller-analytics-api.wildberries.ru", "analytics"
	case "Catalog":
		return "content-api.wildberries.ru", "content"
	case "SellerStocks", "NewOrders", "OrderStatuses":
		return "marketplace-api.wildberries.ru", "marketplace"
	}
	return "", ""
}
