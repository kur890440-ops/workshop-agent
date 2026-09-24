// Package wbmcpfixture provides explicit offline demonstration data. It never
// constructs an HTTP client, reads configuration, or loads a real credential.
package wbmcpfixture

import (
	"context"
	"errors"
	"sync/atomic"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"workshop-agent/internal/integrations/wbmcp"
	wb "workshop-agent/internal/marketplace/wildberries"
)

type API struct {
	Mode  string
	Calls *atomic.Int64
}

func (a API) Prices(context.Context) ([]wb.Price, error) {
	if a.Calls != nil {
		a.Calls.Add(1)
	}
	if a.Mode == "price_error" {
		return nil, wb.Unauthorized
	}
	price := int64(10000)
	if a.Mode == "changed" {
		price = 12000
	}
	return []wb.Price{{NmID: 101, SizeID: 201, VendorCode: "fixture", Currency: "RUB", PriceCents: price}}, nil
}

func (a API) Configured() bool           { return a.Mode != "missing" }
func (a API) ContainsSecret(string) bool { return false }
func (a API) Seller(context.Context) (wb.Seller, error) {
	if a.Calls != nil {
		a.Calls.Add(1)
	}
	id := "day17-fixture"
	if a.Mode == "mismatch" {
		id = "another-cabinet"
	}
	return wb.Seller{ID: id, Name: "Mock WB cabinet"}, nil
}
func (a API) WBStocks(ctx context.Context) ([]wb.Stock, error) {
	if a.Calls != nil {
		a.Calls.Add(1)
	}
	switch a.Mode {
	case "auth":
		return nil, wb.Unauthorized
	case "forbidden":
		return nil, wb.Forbidden
	case "rate":
		return nil, wb.RateLimited
	case "timeout":
		return nil, wb.Timeout
	case "wait":
		<-ctx.Done()
		return nil, ctx.Err()
	case "api":
		return nil, errors.New("synthetic-secret-marker: remote dump MUST NOT escape")
	}
	quantity := int64(12)
	if a.Mode == "changed" {
		quantity = 3
	}
	return []wb.Stock{
		{NmID: 101, ChrtID: 201, WarehouseID: 301, WarehouseName: "Mock warehouse", Quantity: quantity},
		{NmID: 102, ChrtID: 202, WarehouseID: 301, WarehouseName: "Mock warehouse", Quantity: 4},
		{NmID: 103, ChrtID: 203, WarehouseID: 302, WarehouseName: "Mock warehouse 2", Quantity: 0},
	}, nil
}

// Unselected methods deliberately fail: the Day17 flow must not dispatch them.
func (API) Catalog(context.Context) ([]wb.Card, error)                  { return nil, wb.InvalidInput }
func (API) SellerStocks(context.Context, []wb.Card) ([]wb.Stock, error) { return nil, wb.InvalidInput }
func (API) NewOrders(context.Context) ([]wb.Order, error)               { return nil, wb.InvalidInput }
func (API) OrderStatuses(context.Context, []int64) ([]wb.Status, error) { return nil, wb.InvalidInput }

func Server(mode string) (*mcp.Server, error) { return wbmcp.New(API{Mode: mode}) }
