package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	q "workshop-agent/internal/marketplacequery"
)

// Offline model responses exercise the real interpreter/schema boundary. They
// do not claim to benchmark a live model's Russian language accuracy.
func TestMarketplaceNaturalLanguageInterpreterContract(t *testing.T) {
	five := int64(5)
	for _, tc := range []struct {
		text      string
		providers []q.Provider
		threshold bool
		source    q.Source
	}{
		{"остатки WB и Ozon", []q.Provider{q.WB, q.Ozon}, false, q.Auto},
		{"остатки Wildberries и Озон", []q.Provider{q.WB, q.Ozon}, false, q.Auto},
		{"покажи только озон", []q.Provider{q.Ozon}, false, q.Auto},
		{"что заканчивается на маркетплейсах", []q.Provider{q.WB, q.Ozon}, true, q.Auto},
		{"товары меньше 5 штук", []q.Provider{q.WB, q.Ozon}, false, q.Auto},
		{"склады маркетов?", []q.Provider{q.WB, q.Ozon}, false, q.Auto},
		{"че по фбс на озоне?", []q.Provider{q.Ozon}, false, q.Seller},
		{"остатки продавца вб и озон, не FBO", []q.Provider{q.WB, q.Ozon}, false, q.Seller},
		{"что лежит на самих складах WB?", []q.Provider{q.WB}, false, q.Warehouse},
		{"на озоне и у нас, и на складах площадки", []q.Provider{q.Ozon}, false, q.Both},
	} {
		t.Run(tc.text, func(t *testing.T) {
			in := q.MarketplaceQueryIntent{Marketplaces: tc.providers, QueryType: q.Stocks, StockSource: q.Auto, Filters: q.Filters{Operator: "LT", Quantity: &five}, Grouping: "MARKETPLACE", Sorting: "NAME", ComparisonMode: "NONE", IncludeZeroStock: true}
			in.StockSource = tc.source
			if tc.threshold {
				in.Filters = q.Filters{Operator: "LE", UseThreshold: true}
			}
			answer, _ := json.Marshal(StructuredCommand{Action: "marketplace_query", Marketplace: &in})
			client, _ := NewOpenRouterClient("fixture", "https://model.invalid", "fixture")
			calls := 0
			client.HTTPClient = &http.Client{Transport: semanticTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				body, _ := io.ReadAll(r.Body)
				var v any
				if json.Unmarshal(body, &v) != nil {
					t.Fatal("bad request")
				}
				decoded, _ := json.Marshal(v)
				if !strings.Contains(string(decoded), "marketplace_query") || !strings.Contains(string(decoded), tc.text) || !strings.Contains(string(decoded), "FREE-FORM MARKETPLACE LANGUAGE") {
					t.Fatal("marketplace schema not sent")
				}
				response, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": string(answer)}}}})
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(response))), Header: http.Header{}}, nil
			})}
			cmd, _, e := client.Interpret(context.Background(), "{}", tc.text)
			if e != nil || cmd.Action != "marketplace_query" || calls != 1 {
				t.Fatal(cmd, e)
			}
			plan, e := (q.MarketplaceToolRouter{Tools: q.Registry()}).Route(*cmd.Marketplace)
			if e != nil {
				t.Fatal(e)
			}
			if cmd.Marketplace.StockSource != tc.source {
				t.Fatal("source lost")
			}
			if tc.source == q.Seller || (tc.source == q.Auto && len(tc.providers) == 2) {
				for _, step := range plan.Steps {
					if step.Source != q.Seller {
						t.Fatal("unexpected platform stock request", step)
					}
				}
			}
		})
	}
}
func TestMarketplaceRejectsToolInjection(t *testing.T) {
	for _, raw := range []string{
		`{"action":"marketplace_query","tool_name":"wb_get_seller"}`,
		`{"action":"marketplace_query","marketplace":{"tool_name":"delete"}}`,
		`{"action":"marketplace_query","marketplace":{"marketplaces":["WB"],"query_type":"WRITE"}}`,
	} {
		if _, e := DecodeSemantic(raw); e == nil {
			t.Fatal("accepted injection")
		}
	}
}
