package ozon

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const CatalogSource = "CATALOG"
const SellerSource = "OZON_SELLER_STOCK"
const FBOSource = "OZON_FBO_AVAILABLE"
const Success = "SUCCESS"
const Partial = "PARTIAL"

// Identity fields retain their different meanings. One product can have several SKUs.
type Product struct {
	ProductID  int64   `json:"product_id"`
	OfferID    string  `json:"offer_id"`
	SKUs       []int64 `json:"skus"`
	Name       string  `json:"name"`
	UpdatedAt  string  `json:"updated_at"`
	LastSeenAt string  `json:"last_seen_at"`
}
type Stock struct {
	Observation   string `json:"observation"`
	ProductID     int64  `json:"product_id"`
	OfferID       string `json:"offer_id"`
	SKU           int64  `json:"sku"`
	WarehouseID   int64  `json:"warehouse_id"`
	WarehouseName string `json:"warehouse_name"`
	Quantity      int64  `json:"quantity"`
	CapturedAt    string `json:"captured_at"`
	Name          string `json:"name"`
}
type Diagnostic struct {
	Operation string `json:"operation"`
	Endpoint  string `json:"endpoint"`
	Index     int    `json:"index"`
	Field     string `json:"field"`
	Expected  string `json:"expected"`
	Actual    string `json:"actual"` // category, never raw external data
}
type Metrics struct {
	ProductsConsidered   int    `json:"products_considered"`
	IdentifiersValid     int    `json:"identifiers_valid"`
	IdentifiersInvalid   int    `json:"identifiers_invalid"`
	IdentifiersDuplicate int    `json:"identifiers_duplicate"`
	ProductsMissingSKU   int    `json:"products_missing_sku"`
	Batches              int    `json:"batches"`
	CatalogSource        string `json:"catalog_source,omitempty"`
	HTTPRequests         int    `json:"http_request_count"`
	Pages                int    `json:"pages_requested"`
	Received             int    `json:"records_received"`
	Valid                int    `json:"records_valid"`
	Invalid              int    `json:"records_invalid"`
	Saved                int    `json:"records_saved"`
}
type Result struct {
	Provider       string       `json:"provider"`
	Source         string       `json:"source"`
	Status         string       `json:"status"`
	ErrorCode      string       `json:"error_code"`
	CapturedAt     string       `json:"captured_at"`
	LastSuccess    string       `json:"last_success"`
	RetryNotBefore string       `json:"retry_not_before"`
	Cache          string       `json:"cache_state"`
	Dedup          string       `json:"dedup_state"`
	RunID          int64        `json:"run_id"`
	Metrics        Metrics      `json:"metrics"`
	LastMetrics    Metrics      `json:"last_sync_metrics"`
	Diagnostics    []Diagnostic `json:"diagnostics"`
	Products       []Product    `json:"products"`
	Stocks         []Stock      `json:"stocks"`
	Total          int          `json:"total"`
	Offset         int          `json:"offset"`
	UntrustedData  bool         `json:"untrusted_data"`
}
type Input struct {
	Refresh bool `json:"refresh,omitempty"`
	Offset  int  `json:"offset,omitempty"`
}

func validSource(s string) bool { return s == CatalogSource || s == SellerSource || s == FBOSource }
func newResult(source string) Result {
	return Result{Provider: "OZON", Source: source, Status: Success, Cache: "BYPASS", Dedup: "NEW", CapturedAt: time.Now().UTC().Format(time.RFC3339Nano), UntrustedData: true, Products: []Product{}, Stocks: []Stock{}, Diagnostics: []Diagnostic{}}
}
func (r *Result) reject(op operation, index int, field, expected, actual string) {
	r.Metrics.Invalid++
	r.Status = Partial
	if len(r.Diagnostics) < 30 {
		r.Diagnostics = append(r.Diagnostics, Diagnostic{op.name, op.path, index, field, expected, actual})
	}
}
func (r *Result) fail(err error) {
	r.Status = string(Failed)
	r.ErrorCode = string(Failed)
	if e, ok := err.(*Error); ok {
		r.Status = string(e.Code)
		r.ErrorCode = string(e.Code)
		if !e.RetryNotBefore.IsZero() {
			r.RetryNotBefore = e.RetryNotBefore.UTC().Format(time.RFC3339Nano)
		}
	}
	if len(r.Products)+len(r.Stocks) > 0 {
		r.Status = Partial
	}
}
func clean(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
}
func textOK(s string, max int) bool { return len(s) <= max }

// Ozon's protobuf JSON int64s may be numbers or decimal strings; never float64.
type integer int64

func (i *integer) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	n, e := strconv.ParseInt(s, 10, 64)
	if e != nil {
		return e
	}
	*i = integer(n)
	return nil
}

type catalogPage struct {
	Result *struct {
		Items  []json.RawMessage `json:"items"`
		Total  *int              `json:"total"`
		LastID string            `json:"last_id"`
	} `json:"result"`
}
type listItem struct {
	ID    integer `json:"product_id"`
	Offer string  `json:"offer_id"`
}
type detailsPage struct {
	Items []json.RawMessage `json:"items"`
}
type detailItem struct {
	ID      integer `json:"id"`
	Offer   string  `json:"offer_id"`
	Name    string  `json:"name"`
	Updated string  `json:"updated_at"`
	SKU     integer `json:"sku"`
	Sources []struct {
		SKU integer `json:"sku"`
	} `json:"sources"`
}
type sellerPage struct {
	Products []json.RawMessage `json:"products"`
	Cursor   string            `json:"cursor"`
	HasNext  *bool             `json:"has_next"`
}
type stockItem struct {
	ProductID     integer  `json:"product_id"`
	Offer         string   `json:"offer_id"`
	SKU           integer  `json:"sku"`
	Warehouse     integer  `json:"warehouse_id"`
	WarehouseName string   `json:"warehouse_name"`
	Free          *integer `json:"free_stock"`
	Available     *integer `json:"available_stock_count"`
}

func stockRow(raw json.RawMessage, source, stamp string) (Stock, bool) {
	var v stockItem
	if json.Unmarshal(raw, &v) != nil {
		return Stock{}, false
	}
	q := v.Free
	if source == FBOSource {
		q = v.Available
	}
	if v.SKU <= 0 || v.Warehouse <= 0 || q == nil || *q < 0 || v.ProductID < 0 || !textOK(v.Offer, 512) || !textOK(v.WarehouseName, 512) {
		return Stock{}, false
	}
	return Stock{ProductID: int64(v.ProductID), OfferID: clean(v.Offer), SKU: int64(v.SKU), WarehouseID: int64(v.Warehouse), WarehouseName: clean(v.WarehouseName), Quantity: int64(*q), CapturedAt: stamp}, true
}
