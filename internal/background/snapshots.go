package background

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"workshop-agent/internal/auth"
	"workshop-agent/internal/integrations/mcpclient"
	wb "workshop-agent/internal/marketplace/wildberries"
	"workshop-agent/internal/storage"
)

// Prepared rows share one normalization/diff implementation between compute and
// transactional save. Revalidation prevents saving a stale aggregate.
type marketRow struct {
	Tool, Key, Captured, Raw, Old, Delta string
	Nm                                   int64
	Changed                              bool
}

func prepareMarket(q auth.Querier, scope PipelineContext, in MarketInput) (Aggregate, []marketRow, error) {
	if in.PriceStatus == "" {
		in.PriceStatus = "SUCCESS"
	}
	if in.WBInfo.Source == "" {
		in.WBInfo = wb.NewStockBatch(wb.StockWB, in.Stocks.Data, nil).Info
		in.WBInfo.CapturedAt = in.Stocks.FetchedAt
		in.WBInfo.StartedAt = in.Stocks.FetchedAt
	}
	a := Aggregate{PriceStatus: in.PriceStatus, PricesOK: in.PriceStatus == "SUCCESS", StocksOK: in.WBInfo.Status == "SUCCESS", SellerSource: in.Seller.Info, WBSource: in.WBInfo}
	if !a.PricesOK {
		a.Errors++
	}
	if a.SellerSource.Status != "SUCCESS" {
		a.Errors++
	}
	if a.WBSource.Status != "SUCCESS" {
		a.Errors++
	}
	rows := []marketRow{}
	if a.PricesOK && validateMarketPart(in.Prices) != nil || a.StocksOK && validateMarketPart(in.Stocks) != nil {
		return a, nil, ErrInput
	}
	products, pc, sc := map[int64]bool{}, map[int64]bool{}, map[int64]bool{}
	sellerChanged := map[int64]bool{}
	seen := map[string]bool{}
	totals := map[int64]int64{}
	var threshold int64
	if e := q.QueryRow(`SELECT wb_low_stock_threshold FROM workshop_settings WHERE workshop_id=?`, scope.Job.WorkshopID).Scan(&threshold); e != nil {
		return a, nil, e
	}
	add := func(tool, key, captured string, nm int64, v any) error {
		if seen[tool+key] {
			return ErrInput
		}
		seen[tool+key] = true
		products[nm] = true
		raw, e := json.Marshal(v)
		if e != nil {
			return ErrInput
		}
		row := marketRow{Tool: tool, Key: key, Captured: captured, Nm: nm, Raw: string(raw)}
		e = q.QueryRow(`SELECT data_json FROM wb_daily_current WHERE workshop_id=? AND connection_id=? AND source_tool=? AND item_key=?`, scope.Job.WorkshopID, scope.ConnectionID, tool, key).Scan(&row.Old)
		if e != nil && e != sql.ErrNoRows {
			return e
		}
		if e == nil {
			delta := map[string]int64{}
			switch n := v.(type) {
			case wb.Price:
				var p wb.Price
				if json.Unmarshal([]byte(row.Old), &p) != nil {
					return ErrInput
				}
				row.Changed = p.PriceCents != n.PriceCents || p.Currency != n.Currency || !reflect.DeepEqual(p.DiscountedCents, n.DiscountedCents)
				if p.Currency == n.Currency {
					delta["price_cents"] = n.PriceCents - p.PriceCents
					if p.DiscountedCents != nil && n.DiscountedCents != nil {
						delta["discounted_price_cents"] = *n.DiscountedCents - *p.DiscountedCents
					}
				}
				if row.Changed {
					pc[nm] = true
				}
			case wb.Stock:
				var p wb.Stock
				if json.Unmarshal([]byte(row.Old), &p) != nil {
					return ErrInput
				}
				row.Changed = p.Quantity != n.Quantity
				delta["quantity"] = n.Quantity - p.Quantity
				if row.Changed {
					if tool == mcpclient.SellerStocksTool {
						sellerChanged[nm] = true
					} else {
						sc[nm] = true
					}
				}
			}
			dr, _ := json.Marshal(delta)
			row.Delta = string(dr)
		}
		rows = append(rows, row)
		return nil
	}
	for _, p := range in.Prices.Data {
		if e := add(mcpclient.PricesTool, fmt.Sprintf("%d:%d", p.NmID, p.SizeID), in.Prices.FetchedAt, p.NmID, p); e != nil {
			return a, nil, e
		}
	}
	for _, p := range in.Stocks.Data {
		totals[p.NmID] += p.Quantity
		if e := add(mcpclient.StocksTool, fmt.Sprintf("%d:%d:%d", p.NmID, p.ChrtID, p.WarehouseID), in.Stocks.FetchedAt, p.NmID, p); e != nil {
			return a, nil, e
		}
	}
	for _, n := range totals {
		if n == 0 {
			a.ZeroStock++
		} else if n < threshold {
			a.LowStock++
		}
	}
	if in.Seller.Usable() {
		v := mcpclient.Envelope[[]wb.Stock]{Source: "wildberries", FetchedAt: in.Seller.Info.CapturedAt, Complete: true, UntrustedData: true, Data: in.Seller.Rows}
		if validateMarketPart(v) != nil {
			return a, nil, ErrInput
		}
		sellerTotals := map[int64]int64{}
		for _, p := range in.Seller.Rows {
			sellerTotals[p.NmID] += p.Quantity
			if e := add(mcpclient.SellerStocksTool, fmt.Sprintf("%d:%d:%d", p.NmID, p.ChrtID, p.WarehouseID), in.Seller.Info.CapturedAt, p.NmID, p); e != nil {
				return a, nil, e
			}
		}
		// Product-level zero/low requires a complete source; partial missing warehouses remain unknown.
		if in.Seller.Info.Status == "SUCCESS" {
			for _, n := range sellerTotals {
				if n == 0 {
					a.SellerZero++
				} else if n < threshold {
					a.SellerLow++
				}
			}
		}
	}
	var summaryErr error
	a.SellerItems, summaryErr = buildSellerItems(q, scope.Job.WorkshopID, scope.ConnectionID, threshold, in.Seller)
	if summaryErr != nil {
		return a, nil, summaryErr
	}
	if raw, e := json.Marshal(a); e != nil || len(raw) > 65536 {
		return a, nil, ErrInput
	}
	a.SellerChanges = len(sellerChanged)
	a.ProductsCount = len(products)
	a.PriceChanges = len(pc)
	a.StockChanges = len(sc)
	return a, rows, nil
}
func (s *WBDailySyncExecutor) validatePipeline(q auth.Querier, p PipelineContext) error {
	if p.Revision <= 0 || p.RunID <= 0 || p.Owner == "" {
		return ErrScope
	}
	if _, e := check(q, wbJob{p.Job, p.ConnectionID}, p.Revision); e != nil {
		return e
	}
	var n int
	e := q.QueryRow(`SELECT COUNT(*) FROM background_job_runs WHERE id=? AND job_id=? AND workshop_id=? AND job_type=? AND lease_owner=? AND status='running' AND lease_until>?`, p.RunID, p.Job.ID, p.Job.WorkshopID, JobType, p.Owner, s.Now().Unix()).Scan(&n)
	if e != nil || n != 1 {
		return ErrBusy
	}
	return nil
}

// BuildMarketSummary performs only local reads and deterministic computation.
func (s *WBDailySyncExecutor) BuildMarketSummary(ctx context.Context, p PipelineContext, in MarketInput) (Aggregate, error) {
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return Aggregate{}, e
	}
	defer tx.Rollback()
	if e = s.validatePipeline(tx, p); e != nil {
		return Aggregate{}, e
	}
	a, _, e := prepareMarket(tx, p, in)
	return a, e
}

// SaveMarketSnapshot commits BOTH sources and the aggregate atomically. A failed
// step never publishes a partially updated market state. No network in this tx.
func (s *WBDailySyncExecutor) SaveMarketSnapshot(ctx context.Context, p PipelineContext, in SaveInput) (SaveResult, error) {
	out := SaveResult{}
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	if e = s.validatePipeline(tx, p); e != nil {
		return out, e
	}
	a, rows, e := prepareMarket(tx, p, in.Market)
	if e != nil {
		return out, e
	}
	if !reflect.DeepEqual(a, in.Aggregate) {
		return out, ErrInput
	}
	for _, row := range rows {
		if e = ctx.Err(); e != nil {
			return out, e
		}
		if row.Changed {
			if _, e = tx.Exec(`INSERT INTO wb_daily_diffs(run_id,source_tool,item_key,nm_id,old_json,new_json,delta_json) VALUES(?,?,?,?,?,?,?)`, p.RunID, row.Tool, row.Key, row.Nm, row.Old, row.Raw, row.Delta); e != nil {
				return out, e
			}
		}
		if _, e = tx.Exec(`INSERT INTO wb_daily_snapshots(run_id,workshop_id,connection_id,source_tool,item_key,nm_id,captured_at,data_json) VALUES(?,?,?,?,?,?,?,?)`, p.RunID, p.Job.WorkshopID, p.ConnectionID, row.Tool, row.Key, row.Nm, row.Captured, row.Raw); e != nil {
			return out, e
		}
		if _, e = tx.Exec(`INSERT INTO wb_daily_current(workshop_id,connection_id,source_tool,item_key,nm_id,run_id,captured_at,data_json) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(workshop_id,connection_id,source_tool,item_key) DO UPDATE SET nm_id=excluded.nm_id,run_id=excluded.run_id,captured_at=excluded.captured_at,data_json=excluded.data_json`, p.Job.WorkshopID, p.ConnectionID, row.Tool, row.Key, row.Nm, p.RunID, row.Captured, row.Raw); e != nil {
			return out, e
		}
	}
	sellerSaved := 0
	if in.Market.Seller.Info.Source == wb.StockSeller {
		sellerSaved, e = storage.SaveStockBatch(tx, p.Job.WorkshopID, p.ConnectionID, p.RunID, in.Market.Seller)
		if e != nil {
			return out, e
		}
	}
	wbBatch := wb.StockBatch{Info: in.Market.WBInfo, Rows: in.Market.Stocks.Data, Rejections: []wb.StockRejection{}}
	if wbBatch.Info.Source == "" {
		wbBatch = wb.NewStockBatch(wb.StockWB, in.Market.Stocks.Data, nil)
	}
	if _, e = storage.SaveStockBatch(tx, p.Job.WorkshopID, p.ConnectionID, p.RunID, wbBatch); e != nil {
		return out, e
	}
	raw, _ := json.Marshal(a)
	if _, e = tx.Exec(`UPDATE background_job_runs SET aggregate_json=? WHERE id=? AND lease_owner=?`, string(raw), p.RunID, p.Owner); e != nil {
		return out, e
	}
	if e = s.validatePipeline(tx, p); e != nil {
		return out, e
	}
	if e = tx.Commit(); e != nil {
		return out, e
	}
	return SaveResult{Saved: true, PriceRecords: len(in.Market.Prices.Data), StockRecords: len(in.Market.Stocks.Data), SellerRecords: sellerSaved, AggregateSaved: true, CurrentUpdated: len(rows)}, nil
}
