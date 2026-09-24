package background

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"time"

	"workshop-agent/internal/integrations/mcpclient"
	wb "workshop-agent/internal/marketplace/wildberries"
)

type item struct {
	key  string
	nm   int64
	data any
}

func (s *WBDailySyncExecutor) save(j wbJob, revision, run int64, owner, tool, captured string, items []item) (int, error) {
	if _, e := time.Parse(time.RFC3339, captured); e != nil {
		return 0, ErrInput
	}
	tx, e := s.DB.Begin()
	if e != nil {
		return 0, e
	}
	defer tx.Rollback()
	if _, e = check(tx, j, revision); e != nil {
		return 0, e
	}
	var lease int
	if e = tx.QueryRow(`SELECT COUNT(*) FROM background_job_runs WHERE id=? AND lease_owner=? AND status='running' AND lease_until>?`, run, owner, s.Now().Unix()).Scan(&lease); e != nil || lease != 1 {
		return 0, ErrBusy
	}
	changed := map[int64]bool{}
	seen := map[string]bool{}
	for _, row := range items {
		if seen[row.key] {
			return 0, ErrInput
		}
		seen[row.key] = true
		raw, e := json.Marshal(row.data)
		if e != nil {
			return 0, e
		}
		var old string
		err := tx.QueryRow(`SELECT data_json FROM wb_daily_current WHERE workshop_id=? AND connection_id=? AND source_tool=? AND item_key=?`, j.WorkshopID, j.ConnectionID, tool, row.key).Scan(&old)
		if err != nil && err != sql.ErrNoRows {
			return 0, err
		}
		different := false
		delta := map[string]int64{}
		if err == nil {
			if tool == mcpclient.PricesTool {
				var p wb.Price
				if json.Unmarshal([]byte(old), &p) != nil {
					return 0, ErrInput
				}
				n := row.data.(wb.Price)
				different = p.PriceCents != n.PriceCents || p.Currency != n.Currency || !reflect.DeepEqual(p.DiscountedCents, n.DiscountedCents)
				if p.Currency == n.Currency {
					delta["price_cents"] = n.PriceCents - p.PriceCents
					if p.DiscountedCents != nil && n.DiscountedCents != nil {
						delta["discounted_price_cents"] = *n.DiscountedCents - *p.DiscountedCents
					}
				}
			} else {
				var p wb.Stock
				if json.Unmarshal([]byte(old), &p) != nil {
					return 0, ErrInput
				}
				different = p.Quantity != row.data.(wb.Stock).Quantity
				delta["quantity"] = row.data.(wb.Stock).Quantity - p.Quantity
			}
		}
		if different {
			changed[row.nm] = true
			deltaRaw, _ := json.Marshal(delta)
			if _, e = tx.Exec(`INSERT INTO wb_daily_diffs(run_id,source_tool,item_key,nm_id,old_json,new_json,delta_json) VALUES(?,?,?,?,?,?,?)`, run, tool, row.key, row.nm, old, string(raw), string(deltaRaw)); e != nil {
				return 0, e
			}
		}
		if _, e = tx.Exec(`INSERT INTO wb_daily_snapshots(run_id,workshop_id,connection_id,source_tool,item_key,nm_id,captured_at,data_json) VALUES(?,?,?,?,?,?,?,?)`, run, j.WorkshopID, j.ConnectionID, tool, row.key, row.nm, captured, string(raw)); e != nil {
			return 0, e
		}
		if _, e = tx.Exec(`INSERT INTO wb_daily_current(workshop_id,connection_id,source_tool,item_key,nm_id,run_id,captured_at,data_json) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(workshop_id,connection_id,source_tool,item_key) DO UPDATE SET nm_id=excluded.nm_id,run_id=excluded.run_id,captured_at=excluded.captured_at,data_json=excluded.data_json`, j.WorkshopID, j.ConnectionID, tool, row.key, row.nm, run, captured, string(raw)); e != nil {
			return 0, e
		}
	}
	if e = tx.Commit(); e != nil {
		return 0, e
	}
	return len(changed), nil
}
func (s *WBDailySyncExecutor) savePrices(j wbJob, revision, run int64, owner string, v mcpclient.Envelope[[]wb.Price], a *Aggregate, products map[int64]bool) error {
	if !v.Complete || !v.UntrustedData || v.Source != "wildberries" || v.Data == nil || len(v.Data) > 50000 {
		return ErrInput
	}
	items := []item{}
	for _, p := range v.Data {
		if p.NmID <= 0 || p.SizeID <= 0 || p.PriceCents < 0 || len(p.Currency) != 3 || p.DiscountedCents != nil && *p.DiscountedCents < 0 {
			return ErrInput
		}
		items = append(items, item{fmt.Sprintf("%d:%d", p.NmID, p.SizeID), p.NmID, p})
	}
	n, e := s.save(j, revision, run, owner, mcpclient.PricesTool, v.FetchedAt, items)
	if e != nil {
		return e
	}
	a.PriceChanges = n
	for _, p := range v.Data {
		products[p.NmID] = true
	}
	return nil
}
func (s *WBDailySyncExecutor) saveStocks(j wbJob, revision, run int64, owner string, v mcpclient.Envelope[[]wb.Stock], a *Aggregate, products map[int64]bool) error {
	if !v.Complete || !v.UntrustedData || v.Source != "wildberries" || v.Data == nil || len(v.Data) > 50000 {
		return ErrInput
	}
	items := []item{}
	totals := map[int64]int64{}
	for _, p := range v.Data {
		if p.NmID <= 0 || p.ChrtID <= 0 || p.WarehouseID <= 0 || p.Quantity < 0 || p.Quantity > math.MaxInt64-totals[p.NmID] {
			return ErrInput
		}
		totals[p.NmID] += p.Quantity
		items = append(items, item{fmt.Sprintf("%d:%d:%d", p.NmID, p.ChrtID, p.WarehouseID), p.NmID, p})
	}
	var threshold int64
	if e := s.DB.QueryRow(`SELECT wb_low_stock_threshold FROM workshop_settings WHERE workshop_id=?`, j.WorkshopID).Scan(&threshold); e != nil {
		return e
	}
	n, e := s.save(j, revision, run, owner, mcpclient.StocksTool, v.FetchedAt, items)
	if e != nil {
		return e
	}
	a.StockChanges = n
	for id, total := range totals {
		products[id] = true
		if total == 0 {
			a.ZeroStock++
		} else if total < threshold {
			a.LowStock++
		}
	}
	return nil
}
