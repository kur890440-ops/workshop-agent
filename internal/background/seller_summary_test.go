package background

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unicode/utf16"
	wb "workshop-agent/internal/marketplace/wildberries"
)

func TestSellerSummaryGroupsWarehousesSeparatesVariantsAndSources(t *testing.T) {
	s, _, _, _, _, w := fixture(t)
	if _, e := s.DB.Exec(`INSERT INTO marketplace_cards(connection_id,workshop_id,nm_id,title,vendor_code,source_updated_at,fetched_at) VALUES(1,?,1,'Дефлектор','ART','','')`, w); e != nil {
		t.Fatal(e)
	}
	for i, size := range []string{"белый", "чёрный"} {
		if _, e := s.DB.Exec(`INSERT INTO marketplace_variants(connection_id,workshop_id,nm_id,chrt_id,size) VALUES(1,?,1,?,?)`, w, i+1, size); e != nil {
			t.Fatal(e)
		}
	}
	b := wb.NewStockBatch(wb.StockSeller, []wb.Stock{{NmID: 1, ChrtID: 1, WarehouseID: 1, Quantity: 7}, {NmID: 1, ChrtID: 1, WarehouseID: 2, Quantity: 13}, {NmID: 1, ChrtID: 2, WarehouseID: 1, Quantity: 0}}, nil)
	items, e := buildSellerItems(s.DB, w, 1, 5, b)
	if e != nil || len(items) != 2 {
		t.Fatal(items, e)
	}
	if items[0].TotalQuantity != 0 || items[0].Variant != "чёрный" || items[1].TotalQuantity != 20 {
		t.Fatal(items)
	}
	a := Aggregate{SellerItems: items, SellerSource: b.Info, WBSource: wb.NewStockBatch(wb.StockWB, nil, wb.Forbidden).Info}
	text := Summary(Job{Timezone: "Europe/Moscow"}, a, "partial_success")
	for _, v := range []string{"Дефлектор · белый [ART] — 20 шт.", "чёрный [ART] — 0 шт.", "Недоступны для текущего токена."} {
		if !strings.Contains(text, v) {
			t.Fatal(v, text)
		}
	}
	for _, v := range []string{"nm=", "chrt=", "записей 0", "PERMISSION_DENIED", "PARTIAL"} {
		if strings.Contains(text, v) {
			t.Fatal(v)
		}
	}
	// Explicit partial totals contain only received records, missing combinations absent.
	b.Info.Status = "PARTIAL"
	b.Info.Missing = 48
	b.Rows = b.Rows[:1]
	items, e = buildSellerItems(s.DB, w, 1, 5, b)
	if e != nil || len(items) != 1 || items[0].TotalQuantity != 7 || !items[0].Partial {
		t.Fatal(items, e)
	}
}
func TestMorningSummary67ItemsChunkedWithoutLoss(t *testing.T) {
	a := Aggregate{SellerSource: wb.StockInfo{Status: "PARTIAL"}, WBSource: wb.StockInfo{Status: "PERMISSION_DENIED"}}
	for i := 0; i < 67; i++ {
		a.SellerItems = append(a.SellerItems, SellerStockSummaryItem{ProductName: fmt.Sprintf("Позиция-%03d-%s", i, strings.Repeat("😀", 20)), Variant: "размер XL", VendorCode: fmt.Sprintf("SKU%03d", i), TotalQuantity: int64(i), Partial: true})
	}
	chunks := SplitMorningSummary(Summary(Job{Timezone: "Europe/Moscow"}, a, "partial_success"))
	if len(chunks) < 2 {
		t.Fatal("expected multiple messages")
	}
	joined := strings.Join(chunks, "\n")
	for _, c := range chunks {
		if len(utf16.Encode([]rune(c))) > 4096 {
			t.Fatal("Telegram limit")
		}
	}
	for i := 0; i < 67; i++ {
		if strings.Count(joined, fmt.Sprintf("SKU%03d", i)) != 1 {
			t.Fatal("lost/duplicated item", i)
		}
	}
	if !strings.Contains(joined, "Данные получены частично") || strings.Contains(joined, "invalid_response") {
		t.Fatal(joined)
	}
}
func TestSavedMorningSummaryRetrievalUsesSameItems(t *testing.T) {
	s, _, _, _, u, w := fixture(t)
	s.WB.MCP = &sellerSummaryMCP{fakeMCP: fakeMCP{price: 100, stock: 12}}
	if _, e := s.Create(u, w); e != nil {
		t.Fatal(e)
	}
	if e := s.RunNow(context.Background(), u, w); e != nil {
		t.Fatal(e)
	}
	out, e := s.DailySummary(u, w)
	if e != nil || out.Aggregate == nil || len(out.Aggregate.SellerItems) != 1 || out.Aggregate.SellerItems[0].TotalQuantity != 20 {
		t.Fatal(out, e)
	}
	var raw string
	if e = s.DB.QueryRow(`SELECT aggregate_json FROM background_job_runs WHERE id=?`, out.RunID).Scan(&raw); e != nil {
		t.Fatal(e)
	}
	var a Aggregate
	if json.Unmarshal([]byte(raw), &a) != nil || len(a.SellerItems) != 1 || a.SellerItems[0] != out.Aggregate.SellerItems[0] {
		t.Fatal(raw)
	}
}

type sellerSummaryMCP struct{ fakeMCP }

func (f *sellerSummaryMCP) StockSource(ctx context.Context, id string, source wb.StockSource) (wb.StockBatch, error) {
	return wb.NewStockBatch(wb.StockSeller, []wb.Stock{{NmID: 1, ChrtID: 2, WarehouseID: 1, Quantity: 7}, {NmID: 1, ChrtID: 2, WarehouseID: 2, Quantity: 13}}, nil), nil
}
