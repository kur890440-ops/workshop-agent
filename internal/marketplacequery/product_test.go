package marketplacequery

import (
	"strings"
	"testing"
)

func TestProductNameAndPartialTotals(t *testing.T) {
	for _, tc := range []struct {
		name, query string
		want        bool
	}{
		{"Трап душевой", "трапов", true}, {"Трапы душевые", "трап", true}, {"Трапеция", "трап", false}, {"Мыльница", "трап", false}, {"Трап 50 мм", "трап 100", false},
	} {
		if matchesProduct(tc.name, tc.query) != tc.want {
			t.Fatal(tc)
		}
	}
	i := testIntent()
	i.ProductName = "трап"
	i.Total = true
	if e := i.Validate(); e != nil {
		t.Fatal(e)
	}
	r := MarketplaceQueryResult{Trace: Trace{Intent: i}, Sections: []Section{{Tool: Registry()[0], Status: "PARTIAL", Items: []Item{{Row: Row{Name: "Трап", Quantity: 8, Confirmed: true}}, {Row: Row{Name: "Трап", Quantity: 50, Confirmed: false}}}}}}
	text := Render(r)
	if !strings.Contains(text, "полный остаток неизвестен: 8 шт.") || strings.Contains(text, "Всего по этому источнику") {
		t.Fatal(text)
	}
	r.Sections[0].Items = nil
	if strings.Contains(Render(r), ": 0 шт.") {
		t.Fatal("missing became zero")
	}
}
