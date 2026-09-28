package marketplacequery

import "testing"

func testIntent() MarketplaceQueryIntent {
	return MarketplaceQueryIntent{Marketplaces: []Provider{WB, Ozon}, QueryType: Stocks, StockSource: Seller, Filters: Filters{Operator: "NONE"}, Grouping: "MARKETPLACE", Sorting: "NAME", ComparisonMode: "NONE", IncludeZeroStock: true}
}
func TestTrustedRoutingAndSources(t *testing.T) {
	registry := Registry()
	for i := range registry {
		registry[i].Available = true
	}
	for _, tc := range []struct {
		providers []Provider
		source    Source
		want      []string
	}{
		{[]Provider{WB}, Seller, []string{"wb_get_seller_stocks"}},
		{[]Provider{WB}, Warehouse, []string{"wb_get_wb_stocks"}},
		{[]Provider{Ozon}, Seller, []string{"ozon_get_seller_stocks"}},
		{[]Provider{Ozon}, Warehouse, []string{"ozon_get_ozon_stocks"}},
		{[]Provider{WB}, Auto, []string{"wb_get_seller_stocks", "wb_get_wb_stocks"}},
		{[]Provider{WB, Ozon}, Auto, []string{"wb_get_seller_stocks", "ozon_get_seller_stocks"}},
	} {
		in := testIntent()
		in.Marketplaces = tc.providers
		in.StockSource = tc.source
		p, e := (MarketplaceToolRouter{registry}).Route(in)
		if e != nil || len(p.Steps) != len(tc.want) {
			t.Fatal(p, e)
		}
		for i, s := range p.Steps {
			if s.Name != tc.want[i] {
				t.Fatal(p)
			}
		}
	}
	registry[0].Classification = "WRITE"
	if _, e := (MarketplaceToolRouter{registry}).Route(testIntent()); e == nil {
		t.Fatal("write selected")
	}
	registry[0].Classification = "READ_ONLY"
	registry[0].Name = "delete_everything"
	if _, e := (MarketplaceToolRouter{registry}).Route(testIntent()); e == nil {
		t.Fatal("arbitrary name selected")
	}
}
func TestZeroAndComparisonDoNotGuess(t *testing.T) {
	i := testIntent()
	zero := int64(0)
	i.Filters = Filters{Operator: "EQ", Quantity: &zero}
	if accept(i, Item{Row: Row{Quantity: 0, Confirmed: false}}) {
		t.Fatal("unknown zero")
	}
	if !accept(i, Item{Row: Row{Quantity: 0, Confirmed: true}}) {
		t.Fatal("confirmed zero lost")
	}
	i.QueryType = Compare
	i.ComparisonMode = "WB_AVAILABLE_OZON_ZERO"
	s := []Section{{Tool: Tool{Provider: WB, Source: Seller}, Status: "SUCCESS", Items: []Item{{Provider: WB, InternalProductID: 1, Row: Row{Quantity: 10, Confirmed: true, Name: "A"}}}}, {Tool: Tool{Provider: Ozon, Source: Seller}, Status: "SUCCESS", Items: []Item{{Provider: Ozon, InternalProductID: 0, Row: Row{Quantity: 0, Confirmed: true, Name: "A"}}}}}
	if len(compare(i, s)) != 0 {
		t.Fatal("name matching")
	}
	s[1].Items[0].InternalProductID = 1
	if len(compare(i, s)) != 1 {
		t.Fatal("explicit map lost")
	}
	s[1].Status = "PERMISSION_DENIED"
	if len(compare(i, s)) != 0 {
		t.Fatal("denied becomes zero")
	}
	s[1].Status = "PARTIAL"
	if len(compare(i, s)) != 0 {
		t.Fatal("partial total treated complete")
	}
}
