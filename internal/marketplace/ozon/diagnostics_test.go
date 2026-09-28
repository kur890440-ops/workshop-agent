package ozon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestSellerRequestMatchesAuditedPython(t *testing.T) {
	// Exact bodies captured from the audited Python method, not a claimed API schema.
	for _, tc := range []struct {
		name string
		req  SellerStocksRequest
		want string
	}{

		{"sku_array", SellerStocksRequest{Limit: 100, SKUs: []string{"91001", "91002"}}, `{"limit":100,"sku":["91001","91002"]}`},
		{"cursor", SellerStocksRequest{Limit: 25, SKUs: []string{"91001"}, Cursor: "next-page"}, `{"limit":25,"sku":["91001"],"cursor":"next-page"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			c, _ := fixture(t, func(r *http.Request) (*http.Response, error) {
				calls++
				b, _ := io.ReadAll(r.Body)
				var got, want any
				json.Unmarshal(b, &got)
				json.Unmarshal([]byte(tc.want), &want)
				if !reflect.DeepEqual(got, want) || r.URL.Path != seller.path || r.Method != "POST" || r.Header.Get("Content-Type") != "application/json" {
					t.Fatalf("unexpected request %s", b)
				}
				return response(r, 200, `{"products":[],"has_next":false}`), nil
			})
			if _, err := c.SellerStocksPage(context.Background(), tc.req); err != nil {
				t.Fatal(err)
			}
			if calls != 1 {
				t.Fatal(calls)
			}
		})
	}
}

func TestSellerDoesNotSubstituteCatalogIdentifiersOrFanOut(t *testing.T) {
	calls := 0
	c, _ := fixture(t, func(r *http.Request) (*http.Response, error) {
		calls++
		b, _ := io.ReadAll(r.Body)
		if string(b) != `{"sku":["91001","91002"],"limit":100}` {
			t.Fatalf("unexpected SKU/product/offer injection: %s", b)
		}
		return response(r, 200, `{"products":[],"has_next":false}`), nil
	})
	// Invalid/duplicate cached SKU values are filtered before one batched request.
	r := c.Fetch(context.Background(), SellerSource, []int64{0, -1, 91001, 91001, 91002})
	if calls != 1 || r.Status != Success {
		t.Fatalf("calls=%d status=%s", calls, r.Status)
	}
}

func TestSellerBatchAndInvalidSKU(t *testing.T) {
	calls := 0
	c, _ := fixture(t, func(r *http.Request) (*http.Response, error) {
		calls++
		var b SellerStocksRequest
		if json.NewDecoder(r.Body).Decode(&b) != nil || len(b.SKUs) != 100 {
			t.Fatal("batch changed")
		}
		return response(r, 200, `{"products":[],"has_next":false}`), nil
	})
	ids := []string{}
	for i := 1; i <= 100; i++ {
		ids = append(ids, fmt.Sprint(91000+i))
	}
	if _, e := c.SellerStocksPage(context.Background(), SellerStocksRequest{Limit: 100, SKUs: ids}); e != nil {
		t.Fatal(e)
	}
	for _, bad := range [][]string{{"0"}, {""}, {"-1"}, {"offer-A"}, {"01"}, {"91001", "91001"}} {
		if _, e := c.SellerStocksPage(context.Background(), SellerStocksRequest{Limit: 100, SKUs: bad}); e == nil {
			t.Fatal("accepted invalid SKU")
		}
	}
	if calls != 1 {
		t.Fatal("N+1 or invalid network request", calls)
	}
}

func Test400DiagnosticsSanitizedAndNotExposedInResult(t *testing.T) {
	calls := 0
	c, s := fixture(t, func(r *http.Request) (*http.Response, error) {
		calls++
		return response(r, 400, `{"code":3,"message":"invalid limit","details":[{"field":"limit","reason":"must be positive","Api-Key":"other-secret","nested":{"Authorization":"Bearer unknown","Client-Id":"dummy-client"},"echo":"synthetic-ozon-key-never-real","escaped":"\u0073ynthetic-ozon-key-never-real","text":"Bearer unknown-token"}]}`), nil
	})
	_, err := c.SellerStocksPage(context.Background(), SellerStocksRequest{Limit: 100, SKUs: []string{"91001"}})
	e, ok := err.(*Error)
	if !ok || e.Code != BadRequest || e.Diagnostic == nil || calls != 1 {
		t.Fatal("400 handling", err, calls)
	}
	d := s.traces[0].Failure
	if d == nil || string(d.RequestBody) != `{"limit":100,"sku":["91001"]}` || string(d.OzonCode) != "3" || string(d.OzonMessage) != `"invalid limit"` || len(d.OzonDetails) == 0 {
		t.Fatal("missing diagnostics")
	}
	b, _ := json.Marshal(s.traces)
	for _, secret := range []string{fakeKey, "dummy-client", "other-secret", "unknown-token", "Authorization", "Api-Key", "Client-Id"} {
		if strings.Contains(string(b), secret) {
			t.Fatal("secret leaked")
		}
	}
	b, _ = json.Marshal(e)
	if strings.Contains(string(b), "invalid limit") || strings.Contains(string(b), "request_body") {
		t.Fatal("diagnostic exported in error")
	}
	r := newResult(SellerSource)
	r.fail(e)
	b, _ = json.Marshal(r)
	if strings.Contains(string(b), "invalid limit") || strings.Contains(string(b), "request_body") {
		t.Fatal("diagnostic exported in MCP result")
	}
	if !strings.Contains(Format(r), "HTTP 400") {
		t.Fatal("UI hides 400")
	}
}

func TestErrorDiagnosticsBoundsAndJSONRedaction(t *testing.T) {
	c, _ := fixture(t, func(*http.Request) (*http.Response, error) { t.Fatal("network"); return nil, nil })
	for _, body := range []string{fakeKey, `{"message":"` + strings.Repeat("x", maxErrorBody) + `"}`, `{"message":"` + strings.Repeat("x", maxDiagnosticJSON) + `"}`, `{"message":"partial ` + fakeKey} {
		d := c.failureDiagnostic([]byte(`{"limit":100}`), strings.NewReader(body))
		if !strings.Contains(string(d.ResponseBody), "omitted") || len(d.ResponseBody) > maxDiagnosticJSON {
			t.Fatal("unbounded or invalid body")
		}
	}
	c.clientID = "123456"
	d := c.failureDiagnostic([]byte(`{"sku":["123456"],"limit":100}`), strings.NewReader(`{"code":123456,"message":"bad","details":{"credential":"unknown"}}`))
	b, _ := json.Marshal(d)
	if strings.Contains(string(b), "123456") || strings.Contains(string(b), "unknown") {
		t.Fatal("numeric or request secret leak")
	}
}
