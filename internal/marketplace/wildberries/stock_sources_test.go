package wildberries

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestSellerRecordValidation34Received33Valid(t *testing.T) {
	rows := []map[string]any{}
	sizes := []Size{}
	for i := int64(1); i <= 34; i++ {
		sizes = append(sizes, Size{ID: i})
		r := map[string]any{"chrtId": i, "amount": i}
		if i == 17 {
			r["amount"] = "SYNTHETIC-SECRET"
		}
		rows = append(rows, r)
	}
	raw, _ := json.Marshal(map[string]any{"stocks": rows})
	c := client(t, func(r *http.Request) (*http.Response, error) {
		if r.Method == "GET" {
			return response(200, `[{"id":1}]`), nil
		}
		return response(200, string(raw)), nil
	})
	b := c.SellerStockBatch(context.Background(), []Card{{ID: 1, Sizes: sizes}})
	if b.Info.Status != "PARTIAL" || b.Info.Received != 34 || b.Info.Valid != 33 || b.Info.Invalid != 1 || len(b.Rows) != 33 {
		t.Fatalf("%+v", b.Info)
	}
	data, _ := json.Marshal(b.Rejections)
	if len(b.Rejections) == 0 || strings.Contains(string(data), "SECRET") {
		t.Fatal(string(data))
	}
}
func TestSellerMissingIsNotZeroAndContinuesNextWarehouse(t *testing.T) {
	c := client(t, func(r *http.Request) (*http.Response, error) {
		if r.Method == "GET" {
			return response(200, `[{"id":1},{"id":2}]`), nil
		}
		if strings.HasSuffix(r.URL.Path, "/1") {
			return response(200, `{"stocks":[]}`), nil
		}
		return response(200, `{"stocks":[{"chrtId":1,"amount":0}]}`), nil
	})
	b := c.SellerStockBatch(context.Background(), []Card{{ID: 1, Sizes: []Size{{ID: 1}}}})
	if b.Info.Status != "PARTIAL" || b.Info.Missing != 1 || len(b.Rows) != 1 || b.Rows[0].WarehouseID != 2 || b.Rows[0].Quantity != 0 {
		t.Fatal(b)
	}
}
