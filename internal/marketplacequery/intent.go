// Package marketplacequery implements bounded read queries, not a pipeline engine.
package marketplacequery

import (
	"errors"
	"strings"
)

type Provider string
type QueryType string
type Source string
type Comparison string

const (
	WB        Provider  = "WB"
	Ozon      Provider  = "OZON"
	Stocks    QueryType = "STOCKS"
	Products  QueryType = "PRODUCTS"
	Compare   QueryType = "COMPARE"
	Auto      Source    = "AUTO"
	Seller    Source    = "SELLER"
	Warehouse Source    = "MARKETPLACE"
	Both      Source    = "BOTH"
)

type Filters struct {
	Operator     string `json:"operator"`
	Quantity     *int64 `json:"quantity,omitempty"`
	UseThreshold bool   `json:"use_threshold,omitempty"`
	SplitZero    bool   `json:"split_zero,omitempty"`
}
type MarketplaceQueryIntent struct {
	ProductName      string     `json:"product_name,omitempty"`
	Total            bool       `json:"total,omitempty"`
	Marketplaces     []Provider `json:"marketplaces"`
	QueryType        QueryType  `json:"query_type"`
	StockSource      Source     `json:"stock_source"`
	Filters          Filters    `json:"filters"`
	Grouping         string     `json:"grouping"`
	Sorting          string     `json:"sorting"`
	ComparisonMode   Comparison `json:"comparison_mode"`
	IncludeZeroStock bool       `json:"include_zero_stock"`
	RequestedFields  []string   `json:"requested_fields"`
}

func (i MarketplaceQueryIntent) Validate() error {
	bad := errors.New("invalid marketplace intent")
	if len([]rune(i.ProductName)) > 120 || (i.ProductName != "" && strings.TrimSpace(i.ProductName) == "") || (i.Total && i.QueryType != Stocks) {
		return bad
	}
	if len(i.Marketplaces) < 1 || len(i.Marketplaces) > 2 {
		return bad
	}
	seen := map[Provider]bool{}
	for _, p := range i.Marketplaces {
		if (p != WB && p != Ozon) || seen[p] {
			return bad
		}
		seen[p] = true
	}
	if i.QueryType != Stocks && i.QueryType != Products && i.QueryType != Compare {
		return bad
	}
	if i.StockSource != Auto && i.StockSource != Seller && i.StockSource != Warehouse && i.StockSource != Both {
		return bad
	}
	if i.Grouping != "MARKETPLACE" || (i.Sorting != "NAME" && i.Sorting != "QUANTITY_ASC") {
		return bad
	}
	if i.ComparisonMode != "NONE" && i.ComparisonMode != "SIDE_BY_SIDE" && i.ComparisonMode != "WB_AVAILABLE_OZON_ZERO" && i.ComparisonMode != "BOTH_ZERO" {
		return bad
	}
	if (i.QueryType == Compare) != (i.ComparisonMode != "NONE") || (i.QueryType == Compare && (len(seen) != 2 || i.StockSource == Both || i.StockSource == Warehouse)) {
		return bad
	}
	f := i.Filters
	if f.Operator != "NONE" && f.Operator != "LT" && f.Operator != "LE" && f.Operator != "EQ" {
		return bad
	}
	if f.Quantity != nil && (*f.Quantity < 0 || *f.Quantity > 1000000000) {
		return bad
	}
	if f.UseThreshold && (f.Quantity != nil || f.Operator != "LE") {
		return bad
	}
	if f.Operator == "NONE" && (f.Quantity != nil || f.UseThreshold) {
		return bad
	}
	if f.Operator != "NONE" && f.Quantity == nil && !f.UseThreshold {
		return bad
	}
	if i.QueryType == Products && (f.Operator != "NONE" || f.SplitZero) {
		return bad
	}
	fields := map[string]bool{}
	for _, v := range i.RequestedFields {
		if fields[v] || (v != "NAME" && v != "VARIANT" && v != "QUANTITY" && v != "CAPTURED_AT") {
			return bad
		}
		fields[v] = true
	}
	return nil
}

type Tool struct {
	Provider       Provider  `json:"provider"`
	Server         string    `json:"server"`
	Name           string    `json:"tool"`
	Capability     QueryType `json:"capability"`
	Source         Source    `json:"source"`
	Classification string    `json:"classification"`
	Available      bool      `json:"available"`
}

func Registry() []Tool {
	return []Tool{
		{WB, "WB", "wb_get_seller_stocks", Stocks, Seller, "READ_ONLY", false},
		{WB, "WB", "wb_get_wb_stocks", Stocks, Warehouse, "READ_ONLY", false},
		{WB, "WB", "wb_get_products", Products, Auto, "READ_ONLY", false},
		{Ozon, "OZON", "ozon_get_seller_stocks", Stocks, Seller, "READ_ONLY", false},
		{Ozon, "OZON", "ozon_get_ozon_stocks", Stocks, Warehouse, "READ_ONLY", false},
		{Ozon, "OZON", "ozon_list_products", Products, Auto, "READ_ONLY", false},
	}
}

type ExecutionPlan struct {
	Steps []Tool `json:"steps"`
}
type MarketplaceToolRouter struct{ Tools []Tool }

func (r MarketplaceToolRouter) Route(i MarketplaceQueryIntent) (ExecutionPlan, error) {
	var plan ExecutionPlan
	if e := i.Validate(); e != nil {
		return plan, e
	}
	for _, p := range []Provider{WB, Ozon} {
		selected := false
		for _, x := range i.Marketplaces {
			selected = selected || x == p
		}
		if !selected {
			continue
		}
		kind := i.QueryType
		if kind == Compare {
			kind = Stocks
		}
		sources := []Source{i.StockSource}
		if kind == Products {
			sources = []Source{Auto}
		} else if i.StockSource == Both || (i.StockSource == Auto && len(i.Marketplaces) == 1) {
			sources = []Source{Seller, Warehouse}
		} else if i.StockSource == Auto {
			sources = []Source{Seller}
		}
		for _, src := range sources {
			var found *Tool
			for _, candidate := range r.Tools {
				if candidate.Provider == p && candidate.Capability == kind && candidate.Source == src && candidate.Classification == "READ_ONLY" {
					// Only locally defined capability bindings can ever be used; discovery alone grants nothing.
					trusted := false
					for _, t := range Registry() {
						if t.Name == candidate.Name && t.Provider == p && t.Source == src && t.Server == candidate.Server && t.Capability == kind {
							trusted = true
						}
					}
					if trusted {
						v := candidate
						found = &v
						break
					}
				}
			}
			if found == nil {
				return plan, errors.New("read capability not registered")
			}
			plan.Steps = append(plan.Steps, *found)
		}
	}
	return plan, nil
}
