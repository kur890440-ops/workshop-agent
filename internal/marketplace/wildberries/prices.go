package wildberries

import (
	"context"
	"encoding/json"
	"math/big"
	"net/url"
	"strconv"
)

type Price struct {
	NmID            int64  `json:"nmId"`
	SizeID          int64  `json:"sizeId"`
	VendorCode      string `json:"vendorCode"`
	Currency        string `json:"currency"`
	PriceCents      int64  `json:"price_cents"`
	DiscountedCents *int64 `json:"discounted_price_cents"`
}

func cents(n json.Number) (int64, error) {
	r, ok := new(big.Rat).SetString(string(n))
	if !ok || r.Sign() < 0 {
		return 0, InvalidResponse
	}
	r.Mul(r, big.NewRat(100, 1))
	if !r.IsInt() || !r.Num().IsInt64() {
		return 0, InvalidResponse
	}
	return r.Num().Int64(), nil
}
func (c *Client) Prices(ctx context.Context) ([]Price, error) {
	out := []Price{}
	seen := map[[2]int64]bool{}
	for offset := 0; offset < maxRows; offset += 1000 {
		var response struct {
			Error bool `json:"error"`
			Data  struct {
				Goods *[]struct {
					ID       int64  `json:"nmID"`
					Vendor   string `json:"vendorCode"`
					Currency string `json:"currencyIsoCode4217"`
					Sizes    []struct {
						ID         int64        `json:"sizeID"`
						Price      json.Number  `json:"price"`
						Discounted *json.Number `json:"discountedPrice"`
					} `json:"sizes"`
				} `json:"listGoods"`
			} `json:"data"`
		}
		q := url.Values{"limit": {"1000"}, "offset": {strconv.Itoa(offset)}}
		if err := c.requestQuery(ctx, "GET", "discounts-prices-api.wildberries.ru", "/api/v2/list/goods/filter", q, nil, &response); err != nil {
			return nil, err
		}
		if response.Error || response.Data.Goods == nil {
			return nil, InvalidResponse
		}
		goods := *response.Data.Goods
		if len(goods) == 0 {
			return out, nil
		}
		if len(goods) > 1000 {
			return nil, InvalidResponse
		}
		for _, g := range goods {
			if g.ID <= 0 || len(g.Currency) != 3 || len(g.Sizes) == 0 {
				return nil, InvalidResponse
			}
			for _, v := range g.Sizes {
				key := [2]int64{g.ID, v.ID}
				if v.ID <= 0 || seen[key] {
					return nil, InvalidResponse
				}
				seen[key] = true
				p, e := cents(v.Price)
				if e != nil {
					return nil, e
				}
				row := Price{NmID: g.ID, SizeID: v.ID, VendorCode: g.Vendor, Currency: g.Currency, PriceCents: p}
				if v.Discounted != nil {
					d, e := cents(*v.Discounted)
					if e != nil {
						return nil, e
					}
					row.DiscountedCents = &d
				}
				out = append(out, row)
				if len(out) > maxRows {
					return nil, PageLimit
				}
			}
		}
	}
	return nil, PageLimit
}
