// Package wildberries implements only explicitly approved, semantically read-only WB operations.
package wildberries

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Error string

func (e Error) Error() string { return "WB: " + string(e) }

const (
	IdentityMismatch Error = "identity_mismatch"
	NotConfigured    Error = "not_configured"
	InvalidInput     Error = "invalid_input"
	InvalidResponse  Error = "invalid_response"
	Unauthorized     Error = "unauthorized"
	Forbidden        Error = "forbidden"
	RateLimited      Error = "rate_limited"
	Unavailable      Error = "unavailable"
	Timeout          Error = "timeout"
	Cancelled        Error = "cancelled"
	ResponseTooLarge Error = "response_too_large"
	PageLimit        Error = "page_limit"
	RedirectDenied   Error = "redirect_denied"
)

const maxBody = 8 << 20
const maxRows = 50000
const maxPages = 500

// Token is deliberately unexported and cannot be serialized or formatted.
type Client struct {
	token         string
	http          *http.Client
	mu            sync.Mutex
	next          map[string]time.Time
	intervals     map[string]time.Duration
	cooldowns     map[string]RateLimitError
	store         CooldownStore
	identityStore IdentityStore
	sellerCache   SellerCache
	identityGate  chan struct{}
	gates         map[string]chan struct{}
	history       []RequestTrace
	requestBudget *int // diagnostic-only, startup configuration; nil means normal bounds
	clock         func() time.Time
	sleep         func(context.Context, time.Duration) error
}

func (c *Client) String() string   { return "Wildberries(read-only)" }
func (c *Client) GoString() string { return c.String() }
func New(token string) *Client {
	return NewWithProfile(token, "")
}
func NewWithProfile(token, profile string) *Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	c := &Client{token: token, http: &http.Client{Transport: tr, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, next: map[string]time.Time{}, intervals: map[string]time.Duration{"common": time.Minute, "content": 600 * time.Millisecond, "marketplace": 400 * time.Millisecond, "analytics": 20 * time.Second}, cooldowns: map[string]RateLimitError{}, clock: time.Now, sleep: pause}
	if profile == "personal" || profile == "service" {
		c.intervals["common"] = time.Minute
		c.intervals["analytics"] = 20 * time.Second
	}
	c.identityGate = make(chan struct{}, 1)
	c.gates = map[string]chan struct{}{}
	for _, g := range []string{"common", "analytics", "content", "marketplace", "prices"} {
		c.gates[g] = make(chan struct{}, 1)
	}
	c.intervals["prices"] = 600 * time.Millisecond
	return c
}
func (c *Client) Configured() bool {
	if c == nil || len(c.token) < 1 || len(c.token) > 8192 {
		return false
	}
	for _, r := range c.token {
		if r < 33 || r > 126 {
			return false
		}
	}
	return true
}
func (c *Client) ContainsSecret(s string) bool {
	return c != nil && c.token != "" && strings.Contains(s, c.token)
}

type guardKey struct{}

// WithGuard installs the application's scope check, repeated after rate-limit waits.
func WithGuard(ctx context.Context, guard func() error) context.Context {
	return context.WithValue(ctx, guardKey{}, guard)
}
func guard(ctx context.Context) error {
	if ctx.Err() != nil {
		return Cancelled
	}
	if f, ok := ctx.Value(guardKey{}).(func() error); ok {
		return f()
	}
	return nil
}
func pause(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return guard(ctx)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return Cancelled
	case <-t.C:
		return guard(ctx)
	}
}
func (c *Client) wait(ctx context.Context, group string) error {
	return c.waitRate(ctx, group)
}
func endpoint(method, host, path string) (string, bool) {
	switch {
	case method == "GET" && host == "discounts-prices-api.wildberries.ru" && path == "/api/v2/list/goods/filter":
		return "prices", true
	case method == "GET" && host == "common-api.wildberries.ru" && path == "/api/v1/seller-info":
		return "common", true
	case method == "POST" && host == "content-api.wildberries.ru" && path == "/content/v2/get/cards/list":
		return "content", true
	case method == "POST" && host == "seller-analytics-api.wildberries.ru" && path == "/api/analytics/v1/stocks-report/wb-warehouses":
		return "analytics", true
	case host == "marketplace-api.wildberries.ru":
		if method == "GET" && (path == "/api/v3/warehouses" || path == "/api/v3/orders/new") || method == "POST" && path == "/api/v3/orders/status" {
			return "marketplace", true
		}
		if method == "POST" && strings.HasPrefix(path, "/api/v3/stocks/") {
			n, e := strconv.ParseInt(strings.TrimPrefix(path, "/api/v3/stocks/"), 10, 64)
			return "marketplace", e == nil && n > 0
		}
	}
	return "", false
}
func (c *Client) request(ctx context.Context, method, host, path string, body, out any) error {
	return c.requestQuery(ctx, method, host, path, nil, body, out)
}
func (c *Client) requestQuery(ctx context.Context, method, host, path string, query url.Values, body, out any) error {
	group, ok := endpoint(method, host, path)
	if !ok {
		return InvalidInput
	}
	select {
	case c.gates[group] <- struct{}{}:
		defer func() { <-c.gates[group] }()
	case <-ctx.Done():
		return Cancelled
	}
	return c.requestQueryLocked(ctx, method, host, path, query, body, out)
}
func (c *Client) requestQueryLocked(ctx context.Context, method, host, path string, query url.Values, body, out any) error {
	group, ok := endpoint(method, host, path)
	if !ok {
		return InvalidInput
	}
	u := url.URL{Scheme: "https", Host: host, Path: path}
	u.RawQuery = query.Encode()
	if !c.Configured() {
		return NotConfigured
	}
	var raw []byte
	var err error
	if body != nil {
		raw, err = json.Marshal(body)
		if err != nil {
			return InvalidInput
		}
	}
	// Every endpoint in endpoint() is semantically read-only, including POST.
	for attempt := 0; attempt < 3; attempt++ {
		c.mu.Lock()
		exhausted := c.requestBudget != nil && *c.requestBudget <= 0
		if c.requestBudget != nil && !exhausted {
			*c.requestBudget--
		}
		c.mu.Unlock()
		if exhausted {
			c.record(ctx, c.clock(), method, path, 0, attempt, "DIAGNOSTIC_BUDGET", nil, nil)
			return PageLimit
		}
		if err := c.wait(ctx, group); err != nil {
			var rate *RateLimitError
			result := "LOCAL_ERROR"
			if errors.As(err, &rate) {
				result = "BLOCKED_LOCALLY"
			}
			c.record(ctx, c.clock(), method, path, 0, attempt, result, nil, rate)
			return err
		}
		req, err := http.NewRequestWithContext(ctx, method, u.String(), bytes.NewReader(raw))
		if err != nil {
			return InvalidInput
		}
		req.Header.Set("Authorization", c.token)
		req.Header.Set("Accept", "application/json")
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		started := c.clock()
		res, err := c.http.Do(req)
		if err != nil {
			result := "NETWORK_ERROR"
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				result = "TIMEOUT"
			}
			c.record(ctx, started, method, path, 0, attempt, result, nil, nil)
			if ctx.Err() != nil {
				return Cancelled
			}
			if attempt < 2 {
				if e := c.sleep(ctx, time.Duration(1<<attempt)*time.Second); e != nil {
					return e
				}
				continue
			}
			if result == "TIMEOUT" {
				return Timeout
			}
			return Unavailable
		}
		retry := false
		var retryWait time.Duration
		responseErr := func() (responseErr error) {
			obs := observation(res.Header, c.clock(), group, pathLabel(path), res.StatusCode)
			c.observe(obs)
			var responseRate *RateLimitError
			defer func() {
				result := "ERROR"
				if responseErr == nil {
					result = "SUCCESS"
				}
				if res.StatusCode == 429 {
					result = "WB_429"
				}
				c.record(ctx, started, method, path, res.StatusCode, attempt, result, &obs, responseRate)
			}()
			if res.StatusCode == 429 || (obs.Remaining != nil && *obs.Remaining == 0) {
				delay, source := rateDelay(res.Header, c.clock(), attempt)
				if source == "local_backoff" && res.StatusCode != 429 {
					delay = time.Minute
					if group == "prices" {
						delay = 6 * time.Second
					}
					source = "local_interval"
				}
				responseRate = &RateLimitError{Operation: group, RetryAt: c.clock().Add(delay), Source: source}
				if e := c.saveCooldown(*responseRate); e != nil {
					res.Body.Close()
					return e
				}
			}
			if res.StatusCode == 429 {
				res.Body.Close()
				return responseRate
			}
			data, readErr := io.ReadAll(io.LimitReader(res.Body, maxBody+1))
			res.Body.Close()
			if readErr != nil {
				return Unavailable
			}
			if len(data) > maxBody {
				return ResponseTooLarge
			}
			status := res.StatusCode
			if status == 409 && group == "marketplace" {
				if e := c.saveCooldown(RateLimitError{Operation: group, RetryAt: c.clock().Add(10 * c.intervals[group]), Source: "local_interval"}); e != nil {
					return e
				}
			}
			if status >= 300 && status < 400 {
				return RedirectDenied
			}
			if status == 401 {
				return Unauthorized
			}
			if status == 403 {
				return Forbidden
			}
			if status >= 500 {
				delay, source := rateDelay(res.Header, c.clock(), attempt)
				if attempt == 2 {
					return Unavailable
				}
				if delay > 30*time.Second {
					rate := RateLimitError{Operation: group, RetryAt: c.clock().Add(delay), Source: source}
					responseRate = &rate
					if e := c.saveCooldown(rate); e != nil {
						return e
					}
					return &rate
				}
				retry, retryWait = true, delay
				return Unavailable
			}
			if status != 200 {
				return InvalidResponse
			}
			if c.ContainsSecret(string(data)) {
				return InvalidResponse
			}
			if err := json.Unmarshal(data, out); err != nil {
				return InvalidResponse
			}
			decoded, err := json.Marshal(out)
			if err != nil || c.ContainsSecret(string(decoded)) {
				return InvalidResponse
			}
			return nil
		}()
		if retry {
			if e := c.sleep(ctx, retryWait); e != nil {
				return e
			}
			continue
		}
		return responseErr
	}
	return Unavailable
}

type Seller struct {
	ID   string `json:"sid"`
	Name string `json:"name"`
}
type Size struct {
	ID       int64    `json:"chrtID"`
	Size     string   `json:"techSize"`
	Barcodes []string `json:"skus"`
}
type Card struct {
	ID         int64  `json:"nmID"`
	Title      string `json:"title"`
	VendorCode string `json:"vendorCode"`
	UpdatedAt  string `json:"updatedAt"`
	Sizes      []Size `json:"sizes"`
}
type Stock struct {
	NmID          int64  `json:"nmId"`
	ChrtID        int64  `json:"chrtId"`
	WarehouseID   int64  `json:"warehouseId"`
	WarehouseName string `json:"warehouseName"`
	Quantity      int64  `json:"quantity"`
}
type Order struct {
	ID             int64  `json:"id"`
	NmID           int64  `json:"nmId"`
	ChrtID         int64  `json:"chrtId"`
	WarehouseID    int64  `json:"warehouseId"`
	Article        string `json:"article"`
	CreatedAt      string `json:"createdAt"`
	SupplierStatus string `json:"supplierStatus"`
	WBStatus       string `json:"wbStatus"`
}
type Status struct {
	ID             int64  `json:"id"`
	SupplierStatus string `json:"supplierStatus"`
	WBStatus       string `json:"wbStatus"`
}

type cursor struct {
	Limit     int    `json:"limit,omitempty"`
	UpdatedAt string `json:"updatedAt,omitempty"`
	NmID      int64  `json:"nmID,omitempty"`
	Total     int    `json:"total,omitempty"`
}

func (c *Client) Catalog(ctx context.Context) ([]Card, error) {
	var all []Card
	cur := cursor{Limit: 100}
	seen := map[string]bool{}
	variants, barcodes := 0, 0
	for page := 0; page < maxPages; page++ {
		var r struct {
			Cards  []Card  `json:"cards"`
			Cursor *cursor `json:"cursor"`
		}
		body := map[string]any{"settings": map[string]any{"cursor": cur, "filter": map[string]int{"withPhoto": -1}}}
		if err := c.request(ctx, "POST", "content-api.wildberries.ru", "/content/v2/get/cards/list", body, &r); err != nil {
			return all, err
		}
		if r.Cards == nil || r.Cursor == nil || len(r.Cards) > 100 || r.Cursor.Total != len(r.Cards) {
			return all, InvalidResponse
		}
		for _, v := range r.Cards {
			if v.ID <= 0 || len(v.Title) > 1000 || len(v.VendorCode) > 500 || len(v.Sizes) > 1000 {
				return all, InvalidResponse
			}
			for _, s := range v.Sizes {
				variants++
				barcodes += len(s.Barcodes)
				if variants > maxRows || barcodes > maxRows*2 {
					return all, PageLimit
				}
				if s.ID <= 0 || len(s.Barcodes) > 100 || len(s.Size) > 100 {
					return all, InvalidResponse
				}
				for _, b := range s.Barcodes {
					if b == "" || len(b) > 100 {
						return all, InvalidResponse
					}
				}
			}
		}
		all = append(all, r.Cards...)
		if r.Cursor.Total < 100 {
			return all, nil
		}
		key := fmt.Sprintf("%s/%d", r.Cursor.UpdatedAt, r.Cursor.NmID)
		if seen[key] || r.Cursor.UpdatedAt == "" || r.Cursor.NmID <= 0 {
			return all, InvalidResponse
		}
		seen[key] = true
		cur = cursor{Limit: 100, UpdatedAt: r.Cursor.UpdatedAt, NmID: r.Cursor.NmID}
	}
	return all, PageLimit
}
func (c *Client) SellerStocks(ctx context.Context, cards []Card) ([]Stock, error) {
	b := c.SellerStockBatch(ctx, cards)
	if b.Info.Status != "SUCCESS" {
		return b.Rows, &StockPartialError{Batch: b}
	}
	return b.Rows, nil
}

func (c *Client) WBStocks(ctx context.Context) ([]Stock, error) {

	var all []Stock
	seen := map[string]bool{}
	for offset := 0; offset < maxRows; offset += 1000 {
		var response struct {
			Data struct {
				Items []struct {
					NmID          int64  `json:"nmId"`
					ChrtID        int64  `json:"chrtId"`
					WarehouseID   int64  `json:"warehouseId"`
					WarehouseName string `json:"warehouseName"`
					Quantity      *int64 `json:"quantity"`
				} `json:"items"`
			} `json:"data"`
		}
		if err := c.request(ctx, "POST", "seller-analytics-api.wildberries.ru", "/api/analytics/v1/stocks-report/wb-warehouses", map[string]int{"limit": 1000, "offset": offset}, &response); err != nil {
			return all, err
		}
		r := response.Data.Items
		if r == nil || len(r) > 1000 {
			return all, InvalidResponse
		}
		for _, s := range r {
			key := fmt.Sprintf("%d/%d", s.WarehouseID, s.ChrtID)
			if s.NmID <= 0 || s.ChrtID <= 0 || s.WarehouseID <= 0 || s.Quantity == nil || *s.Quantity < 0 || seen[key] || len(s.WarehouseName) > 500 {
				return all, InvalidResponse
			}
			seen[key] = true
			all = append(all, Stock{s.NmID, s.ChrtID, s.WarehouseID, s.WarehouseName, *s.Quantity})
		}
		if len(r) < 1000 {
			return all, nil
		}
	}
	return all, PageLimit
}
func (c *Client) NewOrders(ctx context.Context) ([]Order, error) {
	var r struct {
		Orders []Order `json:"orders"`
	}
	err := c.request(ctx, "GET", "marketplace-api.wildberries.ru", "/api/v3/orders/new", nil, &r)
	if err != nil {
		return nil, err
	}
	if r.Orders == nil || len(r.Orders) > maxRows {
		return nil, InvalidResponse
	}
	seen := map[int64]bool{}
	for _, v := range r.Orders {
		if v.ID <= 0 || v.NmID <= 0 || v.ChrtID <= 0 || v.WarehouseID <= 0 || seen[v.ID] || len(v.Article) > 500 {
			return nil, InvalidResponse
		}
		if _, e := time.Parse(time.RFC3339, v.CreatedAt); e != nil {
			return nil, InvalidResponse
		}
		seen[v.ID] = true
	}
	return r.Orders, nil
}
func (c *Client) OrderStatuses(ctx context.Context, ids []int64) ([]Status, error) {
	if len(ids) == 0 || len(ids) > 1000 {
		return nil, InvalidInput
	}
	wanted := map[int64]bool{}
	for _, id := range ids {
		if id <= 0 || wanted[id] {
			return nil, InvalidInput
		}
		wanted[id] = true
	}
	var r struct {
		Orders []Status `json:"orders"`
	}
	if err := c.request(ctx, "POST", "marketplace-api.wildberries.ru", "/api/v3/orders/status", map[string]any{"orders": ids}, &r); err != nil {
		return nil, err
	}
	for _, s := range r.Orders {
		if !wanted[s.ID] || s.SupplierStatus == "" || s.WBStatus == "" || len(s.SupplierStatus) > 50 || len(s.WBStatus) > 50 {
			return nil, InvalidResponse
		}
		delete(wanted, s.ID)
	}
	if len(wanted) > 0 {
		return nil, InvalidResponse
	}
	return r.Orders, nil
}
