package marketplace

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
	"workshop-agent/internal/integrations/mcpclient"
	"workshop-agent/internal/integrations/wbmcp"
	wb "workshop-agent/internal/marketplace/wildberries"
)

func TestPersistentIdentityRestartAndTokenRotation(t *testing.T) {
	f := setup(t)
	f.attach(t)
	store := cooldownDB{f.ws.DB()}
	sum := sha256.Sum256([]byte(testSecret))
	fingerprint := fmt.Sprintf("%x", sum)
	stamp := time.Now().UTC().Truncate(time.Millisecond)
	if e := store.SaveSeller(fingerprint, wb.SellerCache{Seller: wb.Seller{ID: "seller-a", Name: "Fixture"}, FetchedAt: stamp}); e != nil {
		t.Fatal(e)
	}
	until := stamp.Add(24 * time.Hour)
	if e := store.SaveCooldown(wb.RateLimitError{Operation: "common", RetryAt: until, Source: "wb_retry"}); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 2; i++ {
		client := wb.New(testSecret)
		client.SetIdentityStore(store)
		client.SetCooldownStore(store)
		s, e := New(f.ws.DB(), client)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = s.ReadText(f.sc, "status", 0); e != nil {
			t.Fatal(e)
		}
		seller, e := client.Seller(context.Background())
		if e != nil || seller.ID != "seller-a" {
			t.Fatal(e, seller)
		}
		debug, e := s.DiagnosticsText(f.sc)
		if e != nil || strings.Contains(debug, testSecret) || !strings.Contains(debug, "Seller cached: true") {
			t.Fatal(e, debug)
		}
		for _, r := range client.History() {
			if r.HTTPStatus != 0 || (r.Result != "SUCCESS" || r.Detail != "CACHED") {
				t.Fatal("restart/menu sent HTTP", r)
			}
		}
		s.Close()
	}
	rotated := wb.New(testSecret + "-rotated")
	rotated.SetIdentityStore(store)
	rotated.SetCooldownStore(store)
	if _, e := rotated.Seller(context.Background()); !errors.Is(e, wb.RateLimited) {
		t.Fatal("rotation reused unverified identity", e)
	}
}

func TestMCPUsesPersistentIdentityAndStructuredCooldown(t *testing.T) {
	f := setup(t)
	f.attach(t)
	store := cooldownDB{f.ws.DB()}
	sum := sha256.Sum256([]byte(testSecret))
	if e := store.SaveSeller(fmt.Sprintf("%x", sum), wb.SellerCache{Seller: wb.Seller{ID: "seller-a"}, FetchedAt: time.Now()}); e != nil {
		t.Fatal(e)
	}
	until := time.Now().Add(2 * time.Hour).Truncate(time.Millisecond)
	for _, group := range []string{"common", "prices", "analytics"} {
		if e := store.SaveCooldown(wb.RateLimitError{Operation: group, RetryAt: until, Source: "wb_retry"}); e != nil {
			t.Fatal(e)
		}
	}
	api := wb.New(testSecret)
	api.SetIdentityStore(store)
	api.SetCooldownStore(store)
	server, e := wbmcp.New(api)
	if e != nil {
		t.Fatal(e)
	}
	client, e := mcpclient.NewInMemory(context.Background(), server)
	if e != nil {
		t.Fatal(e)
	}
	defer client.Close()
	ctx := wb.WithTrace(context.Background(), wb.CallMetadata{WorkshopID: f.sc.WorkshopID, Caller: "run_now"})
	_, e = client.Prices(ctx, "seller-a")
	var rate *wb.RateLimitError
	if !errors.As(e, &rate) || rate.Operation != "prices" || !rate.BlockedLocally || !rate.RetryAt.Equal(until) {
		t.Fatal("MCP lost cooldown or called seller-info", e, rate)
	}
	_, e = client.Stocks(ctx, "seller-a")
	if !errors.As(e, &rate) || rate.Operation != "analytics" {
		t.Fatal(e, rate)
	}
	for _, r := range api.History() {
		if r.Caller != "background:WB_DAILY_SYNC:run_now" || r.WorkshopID != f.sc.WorkshopID || r.HTTPStatus != 0 {
			t.Fatal(r)
		}
	}
	var traces int
	if e = f.ws.DB().QueryRow(`SELECT COUNT(*) FROM wb_request_trace WHERE workshop_id=?`, f.sc.WorkshopID).Scan(&traces); e != nil || traces != 2 {
		t.Fatal(e, traces)
	}
}
