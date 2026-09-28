// Package ozon owns the server-side Seller API boundary. It does not expose
// credentials, arbitrary URLs, or write operations to an application tool.
package ozon

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/singleflight"
)

const Host = "https://api-seller.ozon.ru"
const maxResponse = 4 << 20

type Code string

const (
	AuthFailed       Code = "AUTH_FAILED"
	PermissionDenied Code = "PERMISSION_DENIED"
	RateLimited      Code = "RATE_LIMITED"
	InvalidResponse  Code = "INVALID_RESPONSE"
	Failed           Code = "FAILED"
	InvalidInput     Code = "INVALID_INPUT"
	NotConfigured    Code = "NOT_CONFIGURED"
	StorageFailed    Code = "STORAGE_FAILED"
	BadRequest       Code = "BAD_REQUEST"
)

type Error struct {
	Diagnostic     *FailureDiagnostic `json:"-"`
	Code           Code               `json:"code"`
	RetryNotBefore time.Time          `json:"retry_not_before,omitempty"`
}

func (e *Error) Error() string { return string(e.Code) }

// Scope is resolved by the trusted application, never from MCP arguments.
type Scope struct{ WorkshopID, ConnectionID, Revision int64 }
type Metadata struct {
	Batch, Batches, IdentifierCount int
	Caller, Tool, Cache             string
	Page                            int
}
type contextKey struct{}

func TraceMetadata(ctx context.Context) Metadata {
	m, _ := ctx.Value(contextKey{}).(Metadata)
	return m
}

type counterKey struct{}
type requestCounter struct{ n atomic.Int64 }

func WithMetadata(ctx context.Context, m Metadata) context.Context {
	switch m.Caller {
	case "telegram:ozon_products", "telegram:ozon_seller_stocks", "telegram:ozon_side_stocks", "manual_refresh", "mcp_tool":
	default:
		m.Caller = "integration"
	}
	switch m.Tool {
	case "ozon_list_products", "ozon_get_seller_stocks", "ozon_get_ozon_stocks":
	default:
		m.Tool = ""
	}
	switch m.Cache {
	case "HIT", "MISS", "BYPASS":
	default:
		m.Cache = "BYPASS"
	}
	if m.Page < 0 {
		m.Page = 0
	}
	return context.WithValue(ctx, contextKey{}, m)
}

type Trace struct {
	Batch             int                `json:"batch,omitempty"`
	Batches           int                `json:"batches,omitempty"`
	IdentifierCount   int                `json:"identifier_count,omitempty"`
	RefreshMetrics    *Metrics           `json:"refresh_metrics,omitempty"`
	Failure           *FailureDiagnostic `json:"failure,omitempty"`
	Stage             string             `json:"stage"`
	RequestStartedAt  time.Time          `json:"request_started_at"`
	RequestFinishedAt time.Time          `json:"request_finished_at"`
	Timestamp         time.Time          `json:"timestamp"`
	WorkshopID        int64              `json:"workshop_id"`
	ConnectionID      int64              `json:"marketplace_connection_id"`
	Provider          string             `json:"provider"`
	Caller            string             `json:"caller"`
	Operation         string             `json:"operation"`
	Tool              string             `json:"mcp_tool"`
	GoMethod          string             `json:"go_method"`
	HTTPMethod        string             `json:"http_method"`
	Endpoint          string             `json:"endpoint"`
	Attempt           int                `json:"attempt"`
	Cache             string             `json:"cache_state"`
	Dedup             string             `json:"dedup_state"`
	HTTPStatus        int                `json:"http_status"`
	DurationMS        int64              `json:"duration_ms"`
	Page              int                `json:"page"`
	HTTPRequestCount  int                `json:"http_request_count"`
	RecordsReceived   *int               `json:"records_received"`
	RecordsValid      *int               `json:"records_valid"`
	RecordsSaved      *int               `json:"records_saved"`
	RetryNotBefore    time.Time          `json:"retry_not_before,omitempty"`
	RetryAfterSeconds *int64             `json:"retry_after_seconds,omitempty"`
	RateLimit         *int64             `json:"rate_limit,omitempty"`
	RateRemaining     *int64             `json:"rate_remaining,omitempty"`
	Result            string             `json:"result"`
}

// State stores only non-secret control metadata. Implementations must fail
// closed on storage errors and namespace state independently from WB.
type State interface {
	LoadCooldown(context.Context, Scope) (time.Time, error)
	SaveCooldown(context.Context, Scope, time.Time) error
	AppendTrace(context.Context, Trace) error
}
type Client struct {
	clientID, apiKey string
	scope            Scope
	http             *http.Client
	state            State
	guard            func(context.Context, Scope) error
	mu               sync.Mutex
	until            time.Time
	calls            singleflight.Group
	gate             chan struct{}
}

// New binds one immutable credential pair to one resolved connection. Production
// callers cannot change the HTTP transport/host through configuration or tools.
func New(clientID, apiKey string, scope Scope, state State, guard func(context.Context, Scope) error) *Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil // Never inherit an ambient credential-forwarding proxy.
	if tr.TLSClientConfig == nil {
		tr.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	} else {
		tr.TLSClientConfig = tr.TLSClientConfig.Clone()
		tr.TLSClientConfig.InsecureSkipVerify = false
		if tr.TLSClientConfig.MinVersion < tls.VersionTLS12 {
			tr.TLSClientConfig.MinVersion = tls.VersionTLS12
		}
	}
	return &Client{clientID: clientID, apiKey: apiKey, scope: scope, state: state, guard: guard, gate: make(chan struct{}, 1), http: &http.Client{Transport: tr, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}
func (c *Client) Configured() bool {
	return c != nil && c.clientID != "" && c.apiKey != "" && c.scope.WorkshopID > 0 && c.scope.ConnectionID > 0 && c.state != nil && c.guard != nil
}
func (c *Client) Sensitive(text string) bool {
	return c != nil && ((c.apiKey != "" && strings.Contains(text, c.apiKey)) || (c.clientID != "" && strings.Contains(text, c.clientID)))
}
func (c *Client) Close() {
	if c != nil {
		c.http.CloseIdleConnections()
	}
}

type operation struct{ name, path, method string }

var products = operation{"products", "/v3/product/list", "ProductsPage"}
var details = operation{"product_details", "/v3/product/info/list", "ProductDetailsBatch"}
var seller = operation{"seller_stocks", "/v2/product/info/stocks-by-warehouse/fbs", "SellerStocksPage"}
var analytics = operation{"analytics_stocks", "/v1/analytics/stocks", "AnalyticsStocksBatch"}

// These request contracts are confirmed by the audited reference. Responses are
// deliberately not normalized until their official schemas are verified.
type ProductsRequest struct {
	Filter struct {
		Visibility string `json:"visibility"`
	} `json:"filter"`
	Limit  int    `json:"limit"`
	LastID string `json:"last_id"`
}
type DetailsRequest struct {
	ProductIDs []int64 `json:"product_id"`
}
type SellerStocksRequest struct {
	SKUs   []string `json:"sku,omitempty"`
	Limit  int      `json:"limit"`
	Cursor string   `json:"cursor,omitempty"`
}
type AnalyticsRequest struct {
	SKUs []string `json:"skus"`
}

func (c *Client) ProductsPage(ctx context.Context, r ProductsRequest) (json.RawMessage, error) {
	if r.Limit < 1 || r.Limit > 100 || len(r.LastID) > 2048 || r.Filter.Visibility != "ALL" {
		return nil, &Error{Code: InvalidInput}
	}
	return c.read(ctx, products, r)
}
func (c *Client) ProductDetailsBatch(ctx context.Context, r DetailsRequest) (json.RawMessage, error) {
	if len(r.ProductIDs) == 0 || len(r.ProductIDs) > 100 {
		return nil, &Error{Code: InvalidInput}
	}
	seen := map[int64]bool{}
	for _, id := range r.ProductIDs {
		if id <= 0 || seen[id] {
			return nil, &Error{Code: InvalidInput}
		}
		seen[id] = true
	}
	return c.read(ctx, details, r)
}
func validSKUs(ids []string, empty bool) bool {
	if len(ids) > 100 || (!empty && len(ids) == 0) {
		return false
	}
	seen := map[string]bool{}
	for _, id := range ids {
		n, e := strconv.ParseUint(id, 10, 63)
		if e != nil || n == 0 || seen[id] || strconv.FormatUint(n, 10) != id {
			return false
		}
		seen[id] = true
	}
	return true
}
func (c *Client) SellerStocksPage(ctx context.Context, r SellerStocksRequest) (json.RawMessage, error) {
	if r.Limit < 1 || r.Limit > 100 || len(r.Cursor) > 2048 || !validSKUs(r.SKUs, false) {
		return nil, &Error{Code: InvalidInput}
	}
	return c.read(ctx, seller, r)
}
func (c *Client) AnalyticsStocksBatch(ctx context.Context, r AnalyticsRequest) (json.RawMessage, error) {
	if !validSKUs(r.SKUs, false) {
		return nil, &Error{Code: InvalidInput}
	}
	return c.read(ctx, analytics, r)
}

func (c *Client) trace(ctx context.Context, op operation) Trace {
	m, _ := ctx.Value(contextKey{}).(Metadata)
	if m.Caller == "" {
		m.Caller = "integration"
	}
	if m.Cache == "" {
		m.Cache = "BYPASS"
	}
	return Trace{Stage: "HTTP", Timestamp: time.Now().UTC(), WorkshopID: c.scope.WorkshopID, ConnectionID: c.scope.ConnectionID, Provider: "OZON", Caller: m.Caller, Operation: op.name, Tool: m.Tool, GoMethod: op.method, HTTPMethod: "POST", Endpoint: op.path, Cache: m.Cache, Dedup: "NEW", Page: m.Page}
}
func (c *Client) read(ctx context.Context, op operation, body any) (json.RawMessage, error) {
	if !c.Configured() {
		return nil, &Error{Code: NotConfigured}
	}
	if c.guard(ctx, c.scope) != nil {
		return nil, &Error{Code: PermissionDenied}
	}
	b, e := json.Marshal(body)
	if e != nil {
		return nil, &Error{Code: InvalidInput}
	}
	hash := sha256.Sum256(append([]byte(op.path), b...))
	// Credentials/scope are immutable per client. Different bodies/paths never join.
	var executed atomic.Bool
	result := c.calls.DoChan(string(hash[:]), func() (any, error) { executed.Store(true); return c.send(ctx, op, b) })
	select {
	case <-ctx.Done():
		return nil, &Error{Code: Failed}
	case r := <-result:
		if !executed.Load() {
			t := c.trace(ctx, op)
			t.Dedup = "JOINED_EXISTING"
			t.Result = "SHARED_RESULT"
			if e := c.state.AppendTrace(ctx, t); e != nil {
				return nil, &Error{Code: StorageFailed}
			}
		}
		if c.guard(ctx, c.scope) != nil {
			return nil, &Error{Code: PermissionDenied}
		}
		if r.Err != nil {
			return nil, r.Err
		}
		return append(json.RawMessage(nil), r.Val.([]byte)...), nil
	}
}
func number(h http.Header, name string) *int64 {
	n, e := strconv.ParseInt(h.Get(name), 10, 64)
	if e != nil || n < 0 {
		return nil
	}
	return &n
}
func retryTime(h http.Header, now time.Time) time.Time {
	if n := number(h, "Retry-After"); n != nil {
		d := time.Duration(1<<63 - 1)
		if *n <= int64(d/time.Second) {
			d = time.Duration(*n) * time.Second
		}
		return now.Add(d)
	}
	if d, e := http.ParseTime(h.Get("Retry-After")); e == nil && d.After(now) {
		return d
	}
	// Application fallback, NOT a claimed Ozon quota. Never reuse WB cooldowns.
	return now.Add(time.Minute)
}
func (c *Client) send(ctx context.Context, op operation, body []byte) ([]byte, error) {
	select {
	case c.gate <- struct{}{}:
		defer func() { <-c.gate }()
	case <-ctx.Done():
		return nil, &Error{Code: Failed}
	}
	for attempt := 1; attempt <= 2; attempt++ {
		if c.guard(ctx, c.scope) != nil {
			return nil, &Error{Code: PermissionDenied}
		}
		t := c.trace(ctx, op)
		t.Attempt = attempt
		meta := TraceMetadata(ctx)
		t.Batch = meta.Batch
		t.Batches = meta.Batches
		t.IdentifierCount = meta.IdentifierCount
		deadline, e := c.state.LoadCooldown(ctx, c.scope)
		if e != nil {
			return nil, &Error{Code: StorageFailed}
		}
		c.mu.Lock()
		if c.until.After(deadline) {
			deadline = c.until
		}
		c.mu.Unlock()
		if time.Now().Before(deadline) {
			t.Result = "BLOCKED_LOCALLY"
			t.RetryNotBefore = deadline
			if c.state.AppendTrace(ctx, t) != nil {
				return nil, &Error{Code: StorageFailed}
			}
			return nil, &Error{Code: RateLimited, RetryNotBefore: deadline}
		}
		req, e := http.NewRequestWithContext(ctx, "POST", Host+op.path, bytes.NewReader(body))
		if e != nil {
			return nil, &Error{Code: InvalidInput}
		}
		req.Header.Set("Client-Id", c.clientID)
		req.Header.Set("Api-Key", c.apiKey)
		req.Header.Set("Content-Type", "application/json")
		start := time.Now()
		t.RequestStartedAt = start.UTC()
		if counter, ok := ctx.Value(counterKey{}).(*requestCounter); ok {
			counter.n.Add(1)
		}
		resp, e := c.http.Do(req)
		t.HTTPRequestCount = 1
		code := Failed
		retry := false
		var data []byte
		if e != nil {
			retry = ctx.Err() == nil
		} else {
			t.HTTPStatus = resp.StatusCode
			t.RetryAfterSeconds = number(resp.Header, "Retry-After")
			t.RateLimit = number(resp.Header, "RateLimit-Limit")
			t.RateRemaining = number(resp.Header, "RateLimit-Remaining")
			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				t.Failure = c.failureDiagnostic(body, resp.Body)
				t.Failure.Operation = op.name
				t.Failure.Endpoint = op.path
				t.Failure.HTTPStatus = resp.StatusCode
				if len(t.Failure.RequestID) == 0 {
					if id := resp.Header.Get("X-Request-Id"); id != "" {
						b, _ := json.Marshal(id)
						t.Failure.RequestID = c.diagnosticJSON(b)
					}
				}
			}
			switch {
			case resp.StatusCode == 400:
				code = BadRequest
			case resp.StatusCode == 401:
				code = AuthFailed
			case resp.StatusCode == 403:
				code = PermissionDenied
			case resp.StatusCode == 429:
				code = RateLimited
				t.RetryNotBefore = retryTime(resp.Header, time.Now().UTC())
				c.mu.Lock()
				if t.RetryNotBefore.After(c.until) {
					c.until = t.RetryNotBefore
				}
				c.mu.Unlock()
				if c.state.SaveCooldown(ctx, c.scope, t.RetryNotBefore) != nil {
					code = StorageFailed
				}
			case resp.StatusCode >= 500 && resp.StatusCode <= 599:
				retry = true
			case resp.StatusCode == 200:
				data, e = io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
				if e == nil && len(data) <= maxResponse && json.Valid(data) && len(bytes.TrimSpace(data)) > 0 && bytes.TrimSpace(data)[0] == '{' && !c.sensitiveJSON(data) {
					code = ""
				} else {
					code = InvalidResponse
				}
				if e != nil {
					code = Failed
					retry = ctx.Err() == nil
				}
			}
			_ = resp.Body.Close()
		}
		t.DurationMS = time.Since(start).Milliseconds()
		t.RequestFinishedAt = time.Now().UTC()
		if code == "" {
			received, valid := responseCounts(op, data)
			saved := 0
			t.RecordsReceived = &received
			t.RecordsValid = &valid
			t.RecordsSaved = &saved
		}
		t.Result = string(code)
		if code == "" {
			t.Result = "SUCCESS"
		}
		traceCtx, cancelTrace := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		traceErr := c.state.AppendTrace(traceCtx, t)
		cancelTrace()
		if traceErr != nil {
			return nil, &Error{Code: StorageFailed}
		}
		if code == "" {
			return data, nil
		}
		if !retry || attempt == 2 {
			return nil, &Error{Code: code, RetryNotBefore: t.RetryNotBefore, Diagnostic: t.Failure}
		}
		timer := time.NewTimer(250 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, &Error{Code: Failed}
		case <-timer.C:
		}
	}
	return nil, &Error{Code: Failed}
}

func (c *Client) sensitiveJSON(raw []byte) bool {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return true
	}
	var check func(any) bool
	check = func(v any) bool {
		switch x := v.(type) {
		case string:
			return c.Sensitive(x)
		case []any:
			for _, item := range x {
				if check(item) {
					return true
				}
			}
		case map[string]any:
			for k, item := range x {
				if c.Sensitive(k) || check(item) {
					return true
				}
			}
		}
		return false
	}
	return check(value)
}
