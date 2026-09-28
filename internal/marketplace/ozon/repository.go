package ozon

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"workshop-agent/internal/auth"
)

func (s *Service) save(ctx context.Context, a Access, revision int64, r *Result) error {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return &Error{Code: StorageFailed}
	}
	defer tx.Rollback()
	if e = authorized(tx, a, auth.MarketplaceRead); e != nil {
		return e
	}
	c, e := resolved(tx, a, true)
	if e != nil {
		return e
	}
	if c.Revision != revision {
		return auth.ErrDenied
	}
	// No HTTP occurs inside this transaction. Partial rows only upsert; missing
	// values are retained even for complete responses, never synthesized as zero.
	for _, p := range r.Products {
		_, e = tx.Exec(`INSERT INTO marketplace_catalog(connection_id,workshop_id,provider,product_id,offer_id,name,updated_at,last_seen_at) VALUES(?,?,'OZON',?,?,?,?,?) ON CONFLICT(connection_id,product_id) DO UPDATE SET offer_id=excluded.offer_id,name=CASE WHEN excluded.name='' THEN name ELSE excluded.name END,updated_at=CASE WHEN excluded.updated_at='' THEN updated_at ELSE excluded.updated_at END,last_seen_at=excluded.last_seen_at`, a.ConnectionID, a.WorkshopID, p.ProductID, p.OfferID, p.Name, p.UpdatedAt, p.LastSeenAt)
		if e != nil {
			return &Error{Code: StorageFailed}
		}
		for _, sku := range p.SKUs {
			var old int64
			e = tx.QueryRow(`SELECT product_id FROM marketplace_catalog_skus WHERE connection_id=? AND sku=?`, a.ConnectionID, sku).Scan(&old)
			if e == nil && old != p.ProductID {
				r.reject(details, 0, "sku", "stable product identity", "conflicting_identity")
				continue
			}
			if e != nil && !errors.Is(e, sql.ErrNoRows) {
				return &Error{Code: StorageFailed}
			}
			if _, e = tx.Exec(`INSERT OR IGNORE INTO marketplace_catalog_skus(connection_id,workshop_id,provider,product_id,sku) VALUES(?,?,'OZON',?,?)`, a.ConnectionID, a.WorkshopID, p.ProductID, sku); e != nil {
				return &Error{Code: StorageFailed}
			}
		}
	}
	r.Metrics.Saved = len(r.Products) + len(r.Stocks)
	if r.Status == Success {
		r.LastSuccess = r.CapturedAt
	} else {
		_ = tx.QueryRow(`SELECT captured_at FROM marketplace_source_runs WHERE connection_id=? AND workshop_id=? AND source=? AND status='SUCCESS' ORDER BY id DESC LIMIT 1`, a.ConnectionID, a.WorkshopID, r.Source).Scan(&r.LastSuccess)
	}
	// Persist only safe normalized metadata, not full API responses or MCP payloads.
	info := *r
	info.Products = nil
	info.Stocks = nil
	b, e := json.Marshal(info)
	if e != nil {
		return &Error{Code: StorageFailed}
	}
	res, e := tx.Exec(`INSERT INTO marketplace_source_runs(connection_id,workshop_id,provider,source,captured_at,status,info_json) VALUES(?,?,'OZON',?,?,?,?)`, a.ConnectionID, a.WorkshopID, r.Source, r.CapturedAt, r.Status, string(b))
	if e != nil {
		return &Error{Code: StorageFailed}
	}
	id, e := res.LastInsertId()
	if e != nil {
		return &Error{Code: StorageFailed}
	}
	r.RunID = id
	for _, v := range r.Stocks {
		args := []any{id, a.ConnectionID, a.WorkshopID, r.Source, v.SKU, v.WarehouseID, v.ProductID, v.OfferID, v.WarehouseName, v.Quantity, v.CapturedAt}
		_, e = tx.Exec(`INSERT INTO marketplace_stock_history(run_id,connection_id,workshop_id,provider,source,sku,warehouse_id,product_id,offer_id,warehouse_name,quantity,captured_at) VALUES(?,?,?,'OZON',?,?,?,?,?,?,?,?)`, args...)
		if e != nil {
			return &Error{Code: StorageFailed}
		}
		_, e = tx.Exec(`INSERT INTO marketplace_stock_current(run_id,connection_id,workshop_id,provider,source,sku,warehouse_id,product_id,offer_id,warehouse_name,quantity,captured_at) VALUES(?,?,?,'OZON',?,?,?,?,?,?,?,?) ON CONFLICT(connection_id,source,sku,warehouse_id) DO UPDATE SET run_id=excluded.run_id,product_id=excluded.product_id,offer_id=excluded.offer_id,warehouse_name=excluded.warehouse_name,quantity=excluded.quantity,captured_at=excluded.captured_at`, args...)
		if e != nil {
			return &Error{Code: StorageFailed}
		}
	}
	if auditOzon(tx, a, "ozon.refresh."+r.Source, r.Status) != nil {
		return &Error{Code: StorageFailed}
	}
	if tx.Commit() != nil {
		return &Error{Code: StorageFailed}
	}
	return nil
}

func (s *Service) cached(a Access, source string, offset int) (Result, error) {
	r := newResult(source)
	r.CapturedAt = ""
	r.Status = "UNAVAILABLE"
	r.Cache = "MISS"
	r.Offset = offset
	var raw string
	var runID int64
	e := s.db.QueryRow(`SELECT id,info_json FROM marketplace_source_runs WHERE connection_id=? AND workshop_id=? AND provider='OZON' AND source=? ORDER BY id DESC LIMIT 1`, a.ConnectionID, a.WorkshopID, source).Scan(&runID, &raw)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return r, &Error{Code: StorageFailed}
	}
	if e == nil {
		if json.Unmarshal([]byte(raw), &r) != nil {
			return r, &Error{Code: StorageFailed}
		}
		r.Cache = "HIT"
		r.RunID = runID
	}
	r.LastMetrics = r.Metrics
	r.Metrics = Metrics{}
	r.Offset = offset
	r.Products = []Product{}
	r.Stocks = []Stock{}
	if source == CatalogSource {
		if s.db.QueryRow(`SELECT COUNT(*) FROM marketplace_catalog WHERE connection_id=? AND workshop_id=? AND provider='OZON'`, a.ConnectionID, a.WorkshopID).Scan(&r.Total) != nil {
			return r, &Error{Code: StorageFailed}
		}
		rows, e := s.db.Query(`SELECT product_id,offer_id,name,updated_at,last_seen_at FROM marketplace_catalog WHERE connection_id=? AND workshop_id=? AND provider='OZON' ORDER BY product_id LIMIT 15 OFFSET ?`, a.ConnectionID, a.WorkshopID, offset)
		if e != nil {
			return r, &Error{Code: StorageFailed}
		}
		for rows.Next() {
			var p Product
			p.SKUs = []int64{}
			if rows.Scan(&p.ProductID, &p.OfferID, &p.Name, &p.UpdatedAt, &p.LastSeenAt) != nil {
				rows.Close()
				return r, &Error{Code: StorageFailed}
			}
			r.Products = append(r.Products, p)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return r, &Error{Code: StorageFailed}
		}
		// One bulk identity lookup per UI page, never one HTTP/SQL query per item.
		rows, e = s.db.Query(`SELECT product_id,sku FROM marketplace_catalog_skus WHERE connection_id=? AND workshop_id=? AND product_id IN (SELECT product_id FROM marketplace_catalog WHERE connection_id=? AND workshop_id=? ORDER BY product_id LIMIT 15 OFFSET ?) ORDER BY sku`, a.ConnectionID, a.WorkshopID, a.ConnectionID, a.WorkshopID, offset)
		if e != nil {
			return r, &Error{Code: StorageFailed}
		}
		for rows.Next() {
			var pid, sku int64
			if rows.Scan(&pid, &sku) != nil {
				rows.Close()
				return r, &Error{Code: StorageFailed}
			}
			for i := range r.Products {
				if r.Products[i].ProductID == pid {
					r.Products[i].SKUs = append(r.Products[i].SKUs, sku)
				}
			}
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return r, &Error{Code: StorageFailed}
		}
	} else {
		if s.db.QueryRow(`SELECT COUNT(*) FROM marketplace_stock_current WHERE connection_id=? AND workshop_id=? AND provider='OZON' AND source=?`, a.ConnectionID, a.WorkshopID, source).Scan(&r.Total) != nil {
			return r, &Error{Code: StorageFailed}
		}
		rows, e := s.db.Query(`SELECT c.product_id,c.offer_id,c.sku,c.warehouse_id,c.warehouse_name,c.quantity,c.captured_at,COALESCE(p.name,''),c.run_id FROM marketplace_stock_current c LEFT JOIN marketplace_catalog_skus k ON k.connection_id=c.connection_id AND k.workshop_id=c.workshop_id AND k.sku=c.sku LEFT JOIN marketplace_catalog p ON p.connection_id=k.connection_id AND p.product_id=k.product_id WHERE c.connection_id=? AND c.workshop_id=? AND c.provider='OZON' AND c.source=? ORDER BY c.sku,c.warehouse_id LIMIT 15 OFFSET ?`, a.ConnectionID, a.WorkshopID, source, offset)
		if e != nil {
			return r, &Error{Code: StorageFailed}
		}
		for rows.Next() {
			var v Stock
			var observedRun int64
			if rows.Scan(&v.ProductID, &v.OfferID, &v.SKU, &v.WarehouseID, &v.WarehouseName, &v.Quantity, &v.CapturedAt, &v.Name, &observedRun) != nil {
				rows.Close()
				return r, &Error{Code: StorageFailed}
			}
			v.Observation = "AVAILABLE"
			if v.Quantity == 0 {
				v.Observation = "ZERO"
			}
			if observedRun != r.RunID {
				v.Observation = "STALE"
				if r.Status == Success || r.Status == Partial {
					v.Observation = "MISSING"
				}
			}
			r.Stocks = append(r.Stocks, v)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return r, &Error{Code: StorageFailed}
		}
	}
	if e := authorized(s.db, a, auth.MarketplaceRead); e != nil {
		return Result{}, e
	}
	if _, e := resolved(s.db, a, false); e != nil {
		return Result{}, e
	}
	return r, nil
}

func (s *Service) History(a Access) ([]Trace, error) {
	if e := authorized(s.db, a, auth.MarketplaceManage); e != nil {
		return nil, e
	}
	if _, e := resolved(s.db, a, false); e != nil {
		return nil, e
	}
	rows, e := s.db.Query(`SELECT trace_json FROM integration_request_trace WHERE connection_id=? AND workshop_id=? AND provider='OZON' ORDER BY id DESC LIMIT 50`, a.ConnectionID, a.WorkshopID)
	if e != nil {
		return nil, &Error{Code: StorageFailed}
	}
	defer rows.Close()
	out := []Trace{}
	for rows.Next() {
		var raw string
		var t Trace
		if rows.Scan(&raw) != nil || json.Unmarshal([]byte(raw), &t) != nil {
			return nil, &Error{Code: StorageFailed}
		}
		out = append(out, t)
	}
	if rows.Err() != nil {
		return nil, &Error{Code: StorageFailed}
	}
	return out, nil
}
