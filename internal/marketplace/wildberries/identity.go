package wildberries

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"
)

// Token metadata is a local, unverified JWT decode. Never use it to authorize
// a workshop or certify seller identity. No JWT payload is exposed by this API.
func (c *Client) TokenType() string {
	parts := strings.Split(c.token, ".")
	if len(parts) != 3 {
		return "UNKNOWN"
	}
	raw, e := base64.RawURLEncoding.DecodeString(parts[1])
	if e != nil || len(raw) > 8192 {
		return "UNKNOWN"
	}
	var p struct {
		Acc  int     `json:"acc"`
		For  *string `json:"for"`
		Test *bool   `json:"t"`
	}
	if json.Unmarshal(raw, &p) != nil || p.Test == nil {
		return "UNKNOWN"
	}
	switch {
	case p.Acc == 1 && p.For == nil && !*p.Test:
		return "BASE"
	case p.Acc == 2 && p.For == nil && *p.Test:
		return "TEST"
	case p.Acc == 3 && p.For != nil && *p.For == "self" && !*p.Test:
		return "PERSONAL"
	case p.Acc == 4 && p.For != nil && strings.HasPrefix(*p.For, "asid:") && !*p.Test:
		return "SERVICE"
	}
	return "UNKNOWN"
}
func (c *Client) fingerprint() string {
	h := sha256.Sum256([]byte(c.token))
	return hex.EncodeToString(h[:])
}

type SellerCache struct {
	Seller    Seller
	FetchedAt time.Time
}
type IdentityStore interface {
	LoadSeller(string) (SellerCache, error)
	SaveSeller(string, SellerCache) error
}

func (c *Client) SetIdentityStore(s IdentityStore) { c.identityStore = s }
func (c *Client) CachedSeller() (SellerCache, error) {
	if c.identityStore != nil {
		return c.identityStore.LoadSeller(c.fingerprint())
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sellerCache, nil
}
func (c *Client) CachedIdentity(id string) bool {
	v, e := c.CachedSeller()
	return e == nil && id != "" && v.Seller.ID == id
}

type refreshKey struct{}

func RefreshIdentity(ctx context.Context) context.Context {
	return context.WithValue(ctx, refreshKey{}, true)
}
func (c *Client) Seller(ctx context.Context) (Seller, error) {
	// One identity flight, including DB/cache read and HTTP. Other callers reuse
	// its result; the group limiter remains authoritative even on forced refresh.
	select {
	case c.identityGate <- struct{}{}:
		defer func() { <-c.identityGate }()
	case <-ctx.Done():
		return Seller{}, Cancelled
	}
	if e := guard(ctx); e != nil {
		return Seller{}, e
	}
	if force, _ := ctx.Value(refreshKey{}).(bool); !force {
		cached, e := c.CachedSeller()
		if e != nil {
			return Seller{}, Unavailable
		}
		if cached.Seller.ID != "" {
			c.recordCache(ctx)
			return cached.Seller, nil
		}
	}
	var seller Seller
	e := c.request(ctx, "GET", "common-api.wildberries.ru", "/api/v1/seller-info", nil, &seller)
	if e != nil {
		return seller, e
	}
	if seller.ID == "" || len(seller.ID) > 128 || len(seller.Name) > 500 {
		return Seller{}, InvalidResponse
	}
	cached := SellerCache{seller, c.clock()}
	if c.identityStore != nil {
		if e = c.identityStore.SaveSeller(c.fingerprint(), cached); e != nil {
			return Seller{}, Forbidden
		}
	}
	c.mu.Lock()
	c.sellerCache = cached
	c.mu.Unlock()
	return seller, nil
}
func (c *Client) EnsureSeller(ctx context.Context, expected string) error {
	if expected == "" {
		return InvalidInput
	}
	if e := guard(ctx); e != nil {
		return e
	}
	cached, e := c.CachedSeller()
	if e != nil {
		return Unavailable
	}
	if cached.Seller.ID == "" {
		return IdentityMismatch
	}
	if cached.Seller.ID != expected {
		return IdentityMismatch
	}
	return nil
}

type TokenMetadata struct {
	Type       string   `json:"type"`
	ReadOnly   *bool    `json:"read_only"`
	Categories []string `json:"categories"`
}

func (c *Client) TokenMetadata() TokenMetadata {
	out := TokenMetadata{Type: c.TokenType(), Categories: []string{}}
	parts := strings.Split(c.token, ".")
	if len(parts) != 3 {
		return out
	}
	raw, e := base64.RawURLEncoding.DecodeString(parts[1])
	if e != nil || len(raw) > 8192 {
		return out
	}
	var p struct {
		Mask *uint64 `json:"s"`
	}
	if json.Unmarshal(raw, &p) != nil || p.Mask == nil {
		return out
	}
	ro := *p.Mask&(1<<30) != 0
	out.ReadOnly = &ro
	for _, v := range []struct {
		bit  uint
		name string
	}{{1, "Content"}, {2, "Analytics"}, {3, "Prices/Discounts"}, {4, "Marketplace"}, {5, "Statistics"}, {6, "Promotion"}, {7, "Questions/Reviews"}, {9, "Chat"}, {10, "Supplies"}, {11, "Returns"}, {12, "Documents"}, {13, "Finance"}, {16, "Users"}} {
		if *p.Mask&(1<<v.bit) != 0 {
			out.Categories = append(out.Categories, v.name)
		}
	}
	return out
}
