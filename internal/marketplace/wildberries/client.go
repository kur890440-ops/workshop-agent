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
	token     string
	http      *http.Client
	mu        sync.Mutex
	next      map[string]time.Time
	intervals map[string]time.Duration
	cooldowns map[string]RateLimitError
	store     CooldownStore
	clock     func() time.Time
	sleep     func(context.Context, time.Duration) error
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
func retryDelay(h string, attempt int) time.Duration {
	if n, err := strconv.ParseInt(h, 10, 32); err == nil && n >= 0 {
		return time.Duration(n) * time.Second
	}
	if t, err := http.ParseTime(h); err == nil && time.Until(t) > 0 {
		return time.Until(t)
	}
	return time.Duration(1<<attempt) * time.Second
}
func endpoint(method, host, path string) (string, bool) {
	switch {
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
	group, ok := endpoint(method, host, path)
	if !ok {
		return InvalidInput
	}
	u := url.URL{Scheme: "https", Host: host, Path: path}
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
		if err := c.wait(ctx, group); err != nil {
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
		res, err := c.http.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return Cancelled
			}
			var timeout net.Error
			if errors.As(err, &timeout) && timeout.Timeout() {
				return Timeout
			}
			return Unavailable
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
		if status == 409 {
			c.mu.Lock()
			c.next[group] = time.Now().Add(10 * c.intervals[group])
			c.mu.Unlock()
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
		if status == 429 {
			delay, source := rateDelay(res.Header, c.clock(), attempt)
			v := RateLimitError{group, c.clock().Add(delay), source}
			c.mu.Lock()
			if c.next[group].After(v.RetryAt) {
				v.RetryAt = c.next[group]
			}
			c.mu.Unlock()
			if err := c.saveCooldown(v); err != nil {
				return err
			}
			return &v
		}
		if status >= 500 {
			delay := retryDelay(res.Header.Get("Retry-After"), attempt)
			c.mu.Lock()
			next := time.Now().Add(delay)
			if next.After(c.next[group]) {
				c.next[group] = next
			}
			c.mu.Unlock()
			if attempt == 2 {
				if status == 429 {
					return RateLimited
				}
				return Unavailable
			}
			// No shortening Retry-After; the operation deadline cancels excessive waits.
			if err := pause(ctx, delay); err != nil {
				return err
			}
			continue
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

func (c *Client) Seller(ctx context.Context) (Seller, error) {
	var s Seller
	err := c.request(ctx, "GET", "common-api.wildberries.ru", "/api/v1/seller-info", nil, &s)
	if err == nil && (s.ID == "" || len(s.ID) > 128 || len(s.Name) > 500) {
		err = InvalidResponse
	}
	return s, err
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
	var warehouses []struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	if err := c.request(ctx, "GET", "marketplace-api.wildberries.ru", "/api/v3/warehouses", nil, &warehouses); err != nil {
		return nil, err
	}
	if warehouses == nil || len(warehouses) > 1000 {
		return nil, InvalidResponse
	}
	nm := map[int64]int64{}
	ids := []int64{}
	for _, card := range cards {
		for _, s := range card.Sizes {
			if s.ID <= 0 || card.ID <= 0 {
				return nil, InvalidInput
			}
			if _, ok := nm[s.ID]; !ok {
				ids = append(ids, s.ID)
			}
			nm[s.ID] = card.ID
		}
	}
	if len(ids) > maxRows {
		return nil, PageLimit
	}
	var all []Stock
	for _, w := range warehouses {
		if w.ID <= 0 || len(w.Name) > 500 {
			return all, InvalidResponse
		}
		for start := 0; start < len(ids); start += 1000 {
			end := min(start+1000, len(ids))
			chunk := ids[start:end]
			var r struct {
				Stocks []struct {
					ID     int64  `json:"chrtId"`
					Amount *int64 `json:"amount"`
				} `json:"stocks"`
			}
			if err := c.request(ctx, "POST", "marketplace-api.wildberries.ru", fmt.Sprintf("/api/v3/stocks/%d", w.ID), map[string]any{"chrtIds": chunk}, &r); err != nil {
				return all, err
			}
			expected := map[int64]bool{}
			for _, id := range chunk {
				expected[id] = true
			}
			if r.Stocks == nil {
				return all, InvalidResponse
			}
			for _, s := range r.Stocks {
				if !expected[s.ID] || s.Amount == nil || *s.Amount < 0 {
					return all, InvalidResponse
				}
				delete(expected, s.ID)
				all = append(all, Stock{NmID: nm[s.ID], ChrtID: s.ID, WarehouseID: w.ID, WarehouseName: w.Name, Quantity: *s.Amount})
			}
			// A missing requested variant is unknown, never an implicit zero.
			if len(expected) > 0 {
				return all, InvalidResponse
			}
			if len(all) > maxRows {
				return all, PageLimit
			}
		}
	}
	return all, nil
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
