package marketplace

import (
	"context"
	"database/sql"
	"sort"

	"workshop-agent/internal/auth"
	wb "workshop-agent/internal/marketplace/wildberries"
)

func (s *Service) sync(ctx context.Context, j *job, kind string) {
	var count int
	var err error
	var save func(*sql.Tx, string) error
	switch kind {
	case "catalog":
		var cards []wb.Card
		cards, err = s.api.Catalog(ctx)
		count = len(cards)
		save = func(tx *sql.Tx, stamp string) error { return saveCards(tx, j.scope, cards, stamp) }
	case "seller_stocks", "wb_stocks":
		var stocks []wb.Stock
		if kind == "wb_stocks" {
			stocks, err = s.api.WBStocks(ctx)
		} else {
			var cards []wb.Card
			cards, err = s.catalogSnapshot(j.scope)
			if err == nil {
				stocks, err = s.api.SellerStocks(ctx, cards)
			}
		}
		count = len(stocks)
		save = func(tx *sql.Tx, stamp string) error { return saveStocks(tx, j.scope, kind, stocks, stamp) }
	case "orders":
		var orders []wb.Order
		orders, err = s.loadOrders(ctx, j.scope)
		count = len(orders)
		save = func(tx *sql.Tx, stamp string) error { return saveOrders(tx, j.scope, orders, stamp) }
	}
	if ctx.Err() != nil {
		err = wb.Cancelled
	}
	if e := s.finish(j, kind, count, err, save); e == ErrStorage {
		_ = s.finish(j, kind, count, e, nil)
	}
}
func saveCards(tx *sql.Tx, sc Scope, cards []wb.Card, stamp string) error {
	for _, q := range []string{`UPDATE marketplace_cards SET present=0 WHERE connection_id=? AND workshop_id=?`, `UPDATE marketplace_variants SET present=0 WHERE connection_id=? AND workshop_id=?`} {
		if _, e := tx.Exec(q, sc.ConnectionID, sc.WorkshopID); e != nil {
			return e
		}
	}
	seen := map[int64]bool{}
	sizes := map[int64]bool{}
	for _, c := range cards {
		if seen[c.ID] {
			return wb.InvalidResponse
		}
		seen[c.ID] = true
		_, err := tx.Exec(`INSERT INTO marketplace_cards(connection_id,workshop_id,nm_id,title,vendor_code,source_updated_at,fetched_at,present) VALUES(?,?,?,?,?,?,?,1) ON CONFLICT(connection_id,nm_id) DO UPDATE SET title=excluded.title,vendor_code=excluded.vendor_code,source_updated_at=excluded.source_updated_at,fetched_at=excluded.fetched_at,present=1`, sc.ConnectionID, sc.WorkshopID, c.ID, c.Title, c.VendorCode, c.UpdatedAt, stamp)
		if err != nil {
			return err
		}
		for _, v := range c.Sizes {
			if sizes[v.ID] {
				return wb.InvalidResponse
			}
			sizes[v.ID] = true
			_, err = tx.Exec(`INSERT INTO marketplace_variants(connection_id,workshop_id,nm_id,chrt_id,size,present) VALUES(?,?,?,?,?,1) ON CONFLICT(connection_id,chrt_id) DO UPDATE SET size=excluded.size,present=1 WHERE nm_id=excluded.nm_id`, sc.ConnectionID, sc.WorkshopID, c.ID, v.ID, v.Size)
			if err != nil {
				return err
			}
			var oldNm int64
			if tx.QueryRow(`SELECT nm_id FROM marketplace_variants WHERE connection_id=? AND chrt_id=?`, sc.ConnectionID, v.ID).Scan(&oldNm) != nil || oldNm != c.ID {
				return wb.InvalidResponse
			}
			if _, err = tx.Exec(`DELETE FROM marketplace_barcodes WHERE connection_id=? AND chrt_id=?`, sc.ConnectionID, v.ID); err != nil {
				return err
			}
			for _, b := range v.Barcodes {
				if _, err = tx.Exec(`INSERT INTO marketplace_barcodes(connection_id,chrt_id,barcode) VALUES(?,?,?) ON CONFLICT DO NOTHING`, sc.ConnectionID, v.ID, b); err != nil {
					return err
				}
			}
		}
	}
	// Mappings remain explicit, but vanished variants/barcodes are no longer active.
	_, err := tx.Exec(`UPDATE marketplace_mappings SET status=CASE WHEN EXISTS(SELECT 1 FROM marketplace_variants v WHERE v.connection_id=marketplace_mappings.connection_id AND v.chrt_id=marketplace_mappings.chrt_id AND v.present=1) AND (barcode='' OR EXISTS(SELECT 1 FROM marketplace_barcodes b WHERE b.connection_id=marketplace_mappings.connection_id AND b.chrt_id=marketplace_mappings.chrt_id AND b.barcode=marketplace_mappings.barcode)) THEN 'active' ELSE 'missing' END WHERE connection_id=? AND workshop_id=?`, sc.ConnectionID, sc.WorkshopID)
	return err
}
func saveStocks(tx *sql.Tx, sc Scope, kind string, stocks []wb.Stock, stamp string) error {
	if _, err := tx.Exec(`DELETE FROM marketplace_stocks WHERE connection_id=? AND workshop_id=? AND kind=?`, sc.ConnectionID, sc.WorkshopID, kind); err != nil {
		return err
	}
	for _, v := range stocks {
		_, err := tx.Exec(`INSERT INTO marketplace_stocks(connection_id,workshop_id,kind,warehouse_id,warehouse_name,nm_id,chrt_id,quantity,fetched_at) VALUES(?,?,?,?,?,?,?,?,?)`, sc.ConnectionID, sc.WorkshopID, kind, v.WarehouseID, v.WarehouseName, v.NmID, v.ChrtID, v.Quantity, stamp)
		if err != nil {
			return err
		}
	}
	return nil
}
func saveOrders(tx *sql.Tx, sc Scope, orders []wb.Order, stamp string) error {
	for _, v := range orders {
		_, err := tx.Exec(`INSERT INTO marketplace_orders(connection_id,workshop_id,order_id,nm_id,chrt_id,warehouse_id,article,created_at,supplier_status,wb_status,fetched_at) VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(connection_id,order_id) DO UPDATE SET supplier_status=excluded.supplier_status,wb_status=excluded.wb_status,fetched_at=excluded.fetched_at`, sc.ConnectionID, sc.WorkshopID, v.ID, v.NmID, v.ChrtID, v.WarehouseID, v.Article, v.CreatedAt, v.SupplierStatus, v.WBStatus, stamp)
		if err != nil {
			return err
		}
	}
	return nil
}
func (s *Service) catalogSnapshot(sc Scope) ([]wb.Card, error) {
	var state string
	if s.db.QueryRow(`SELECT state FROM marketplace_sync WHERE connection_id=? AND workshop_id=? AND kind='catalog'`, sc.ConnectionID, sc.WorkshopID).Scan(&state) != nil || state != "succeeded" {
		return nil, wb.InvalidResponse
	}
	rows, err := s.db.Query(`SELECT c.nm_id,v.chrt_id FROM marketplace_cards c JOIN marketplace_variants v ON v.connection_id=c.connection_id AND v.nm_id=c.nm_id WHERE c.connection_id=? AND c.workshop_id=? AND c.present=1 AND v.present=1 ORDER BY c.nm_id,v.chrt_id LIMIT 50001`, sc.ConnectionID, sc.WorkshopID)
	if err != nil {
		return nil, ErrStorage
	}
	defer rows.Close()
	var cards []wb.Card
	count := 0
	for rows.Next() {
		var nm, chrt int64
		if rows.Scan(&nm, &chrt) != nil {
			return nil, ErrStorage
		}
		count++
		if count > 50000 {
			return nil, wb.PageLimit
		}
		if len(cards) == 0 || cards[len(cards)-1].ID != nm {
			cards = append(cards, wb.Card{ID: nm})
		}
		cards[len(cards)-1].Sizes = append(cards[len(cards)-1].Sizes, wb.Size{ID: chrt})
	}
	if rows.Err() != nil {
		return nil, ErrStorage
	}
	return cards, nil
}
func (s *Service) loadOrders(ctx context.Context, sc Scope) ([]wb.Order, error) {
	orders, err := s.api.NewOrders(ctx)
	if err != nil {
		return orders, err
	}
	byID := map[int64]wb.Order{}
	for _, v := range orders {
		byID[v.ID] = v
	}
	rows, err := s.db.Query(`SELECT order_id,nm_id,chrt_id,warehouse_id,article,created_at,supplier_status,wb_status FROM marketplace_orders WHERE connection_id=? AND workshop_id=? ORDER BY order_id LIMIT 50001`, sc.ConnectionID, sc.WorkshopID)
	if err != nil {
		return nil, ErrStorage
	}
	for rows.Next() {
		var v wb.Order
		if rows.Scan(&v.ID, &v.NmID, &v.ChrtID, &v.WarehouseID, &v.Article, &v.CreatedAt, &v.SupplierStatus, &v.WBStatus) != nil {
			rows.Close()
			return nil, ErrStorage
		}
		if _, ok := byID[v.ID]; !ok {
			byID[v.ID] = v
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, ErrStorage
	}
	if len(byID) > 50000 {
		return nil, wb.PageLimit
	}
	ids := make([]int64, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	result := make([]wb.Order, 0, len(ids))
	for start := 0; start < len(ids); start += 1000 {
		statuses, e := s.api.OrderStatuses(ctx, ids[start:min(start+1000, len(ids))])
		if e != nil {
			return result, e
		}
		for _, v := range statuses {
			o := byID[v.ID]
			o.SupplierStatus = v.SupplierStatus
			o.WBStatus = v.WBStatus
			result = append(result, o)
		}
	}
	return result, nil
}

type VariantView struct {
	NmID, ChrtID                                int64
	Title, VendorCode, Size, Barcode, FetchedAt string
	ProductID                                   int64
	MappingStatus                               string
}
type StockView struct {
	wb.Stock
	Kind, FetchedAt string
}
type OrderView struct {
	wb.Order
	FetchedAt string
}

func (s *Service) readScope(sc Scope, offset int) error {
	if offset < 0 || offset > 50000 {
		return ErrInput
	}
	if err := authorize(s.db, sc, auth.MarketplaceRead); err != nil {
		return err
	}
	_, err := connection(s.db, sc, false)
	return err
}

// Lists are bounded local views. Freshness/completeness is supplied by Status, not inferred from rows.
func (s *Service) ListVariants(sc Scope, offset int) ([]VariantView, error) {
	if err := s.readScope(sc, offset); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT c.nm_id,v.chrt_id,c.title,c.vendor_code,v.size,COALESCE(b.barcode,''),c.fetched_at,COALESCE(m.product_id,0),COALESCE(m.status,'unmapped') FROM marketplace_cards c JOIN marketplace_variants v ON v.connection_id=c.connection_id AND v.nm_id=c.nm_id LEFT JOIN marketplace_barcodes b ON b.connection_id=v.connection_id AND b.chrt_id=v.chrt_id LEFT JOIN marketplace_mappings m ON m.connection_id=v.connection_id AND m.chrt_id=v.chrt_id AND (m.barcode=COALESCE(b.barcode,'') OR m.barcode='') WHERE c.connection_id=? AND c.workshop_id=? AND c.present=1 AND v.present=1 ORDER BY c.nm_id,v.chrt_id,b.barcode LIMIT 10 OFFSET ?`, sc.ConnectionID, sc.WorkshopID, offset)
	if err != nil {
		return nil, ErrStorage
	}
	defer rows.Close()
	var out []VariantView
	for rows.Next() {
		var v VariantView
		if rows.Scan(&v.NmID, &v.ChrtID, &v.Title, &v.VendorCode, &v.Size, &v.Barcode, &v.FetchedAt, &v.ProductID, &v.MappingStatus) != nil {
			return nil, ErrStorage
		}
		out = append(out, v)
	}
	if rows.Err() != nil {
		return nil, ErrStorage
	}
	return out, nil
}
func (s *Service) ListStocks(sc Scope, offset int) ([]StockView, error) {
	if err := s.readScope(sc, offset); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT kind,warehouse_id,warehouse_name,nm_id,chrt_id,quantity,fetched_at FROM marketplace_stocks WHERE connection_id=? AND workshop_id=? ORDER BY kind,warehouse_id,chrt_id LIMIT 10 OFFSET ?`, sc.ConnectionID, sc.WorkshopID, offset)
	if err != nil {
		return nil, ErrStorage
	}
	defer rows.Close()
	var out []StockView
	for rows.Next() {
		var v StockView
		if rows.Scan(&v.Kind, &v.WarehouseID, &v.WarehouseName, &v.NmID, &v.ChrtID, &v.Quantity, &v.FetchedAt) != nil {
			return nil, ErrStorage
		}
		out = append(out, v)
	}
	if rows.Err() != nil {
		return nil, ErrStorage
	}
	return out, nil
}
func (s *Service) ListOrders(sc Scope, offset int) ([]OrderView, error) {
	if err := s.readScope(sc, offset); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT order_id,nm_id,chrt_id,warehouse_id,article,created_at,supplier_status,wb_status,fetched_at FROM marketplace_orders WHERE connection_id=? AND workshop_id=? ORDER BY order_id DESC LIMIT 10 OFFSET ?`, sc.ConnectionID, sc.WorkshopID, offset)
	if err != nil {
		return nil, ErrStorage
	}
	defer rows.Close()
	var out []OrderView
	for rows.Next() {
		var v OrderView
		if rows.Scan(&v.ID, &v.NmID, &v.ChrtID, &v.WarehouseID, &v.Article, &v.CreatedAt, &v.SupplierStatus, &v.WBStatus, &v.FetchedAt) != nil {
			return nil, ErrStorage
		}
		out = append(out, v)
	}
	if rows.Err() != nil {
		return nil, ErrStorage
	}
	return out, nil
}
