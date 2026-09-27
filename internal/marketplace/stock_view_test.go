package marketplace

import (
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
	wb "workshop-agent/internal/marketplace/wildberries"
	"workshop-agent/internal/storage"
)

func TestStockDisplayMetadataPaginationAndStatus(t *testing.T) {
	f := setup(t)
	f.attach(t)
	_, e := f.ws.DB().Exec(`INSERT INTO workshop_settings(workshop_id,timezone) VALUES(?,'Asia/Yekaterinburg') ON CONFLICT(workshop_id) DO UPDATE SET timezone=excluded.timezone`, f.sc.WorkshopID)
	if e != nil {
		t.Fatal(e)
	}
	rows := []wb.Stock{}
	cards := []wb.Card{}
	for i := int64(1); i <= 67; i++ {
		cards = append(cards, wb.Card{ID: i, Title: fmt.Sprintf("Товар %02d", i), VendorCode: fmt.Sprintf("ART-%02d", i), Sizes: []wb.Size{{ID: i, Size: "M"}}})
		rows = append(rows, wb.Stock{NmID: i, ChrtID: i, WarehouseID: 777, WarehouseName: "Основной", Quantity: i})
	}
	tx, e := f.ws.DB().Begin()
	if e != nil {
		t.Fatal(e)
	}
	if e = saveCards(tx, f.sc, cards, "2026-09-25T14:02:00Z"); e != nil {
		t.Fatal(e)
	}
	old := wb.NewStockBatch(wb.StockSeller, nil, wb.InvalidResponse)
	old.Info.CapturedAt = "2026-09-25T12:00:00Z"
	if _, e = storage.SaveStockBatch(tx, f.sc.WorkshopID, 1, 0, old); e != nil {
		t.Fatal(e)
	}
	b := wb.NewStockBatch(wb.StockSeller, rows, nil)
	b.Info.CapturedAt = "2026-09-25T14:02:00Z"
	if _, e = storage.SaveStockBatch(tx, f.sc.WorkshopID, 1, 0, b); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	before := f.api.calls
	seen := map[string]bool{}
	for offset := 0; offset < 67; offset += 15 {
		p, e := f.s.StockPage(f.sc, "seller_stocks", offset)
		if e != nil {
			t.Fatal(e)
		}
		expected := 15
		if offset == 60 {
			expected = 7
		}
		if len(p.Items) != expected || p.Total != 67 {
			t.Fatal(p)
		}
		for _, x := range p.Items {
			if seen[x.ProductName] {
				t.Fatal("duplicate")
			}
			seen[x.ProductName] = true
		}
	}
	text, e := f.s.StockSourcesText(f.sc, "seller_stocks", 0)
	if e != nil {
		t.Fatal(e)
	}
	for _, v := range []string{"Товар 01", "ART-01", "Размер: M", "Склад: Основной", "Остаток: 1 шт.", "25.09.2026 19:02", "Данные получены", "Показано 1–15 из 67"} {
		if !strings.Contains(text, v) {
			t.Fatal(v, text)
		}
	}
	for _, v := range []string{"nm=", "chrtID", "warehouseID", "invalid_response", "2026-09-25T"} {
		if strings.Contains(text, v) {
			t.Fatal(v, text)
		}
	}
	if f.api.calls != before {
		t.Fatal("display called API")
	}
	if len(utf16.Encode([]rune(text))) > 4096 {
		t.Fatal("Telegram limit")
	}
	// Metadata fallback is local and does not alter the source records.
	_, e = f.ws.DB().Exec(`UPDATE marketplace_cards SET title='' WHERE nm_id=1`)
	if e != nil {
		t.Fatal(e)
	}
	p, e := f.s.StockPage(f.sc, "seller_stocks", 0)
	if e != nil || p.Items[0].ProductName != "ART-01" {
		t.Fatal(p, e)
	}
	_, e = f.ws.DB().Exec(`UPDATE marketplace_cards SET title='',vendor_code='' WHERE nm_id=1`)
	if e != nil {
		t.Fatal(e)
	}
	_, e = f.ws.DB().Exec(`UPDATE marketplace_stocks SET warehouse_name='' WHERE nm_id=1`)
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for offset := 0; offset < 67; offset += 15 {
		p, _ = f.s.StockPage(f.sc, "seller_stocks", offset)
		for _, x := range p.Items {
			if x.ProductName == "Товар WB #1" {
				found = true
				if x.WarehouseName != "Склад продавца #777" {
					t.Fatal(x)
				}
			}
		}
	}
	if !found {
		t.Fatal("fallback missing")
	}
}
func TestStockTimeAndLongFields(t *testing.T) {
	loc, _ := time.LoadLocation("Europe/Moscow")
	if displayTime("2026-09-25T14:02:00Z", loc) != "25.09.2026 17:02" {
		t.Fatal("timezone")
	}
	if len(utf16.Encode([]rune(shortStock(strings.Repeat("😀", 60))))) > 25 {
		t.Fatal("unicode limit")
	}
}
