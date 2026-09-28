package marketplacequery

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"workshop-agent/internal/auth"
	"workshop-agent/internal/invariants"
)

type Repository struct {
	DB        *sql.DB
	Sensitive func(string) bool
}

func (r *Repository) Binding(s Scope, p Provider) (int64, int64) {
	provider := "wildberries"
	if p == Ozon {
		provider = "OZON"
	}
	var id, rev int64
	if r.DB.QueryRow(`SELECT id,revision FROM marketplace_connections WHERE workshop_id=? AND provider=? AND enabled=1`, s.WorkshopID, provider).Scan(&id, &rev) != nil {
		return 0, 0
	}
	return id, rev
}
func (r *Repository) Variants(s Scope, p Provider, connection int64) (map[int64]string, error) {
	out := map[int64]string{}
	if p != WB || connection == 0 {
		return out, nil
	}
	rows, e := r.DB.Query(`SELECT chrt_id,size FROM marketplace_variants WHERE workshop_id=? AND connection_id=?`, s.WorkshopID, connection)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var name string
		if e = rows.Scan(&id, &name); e != nil {
			return nil, e
		}
		out[id] = name
	}
	return out, rows.Err()
}

func (r *Repository) Authorize(s Scope) error {
	if auth.Require(r.DB, s.UserID, s.WorkshopID, auth.MarketplaceRead) != nil {
		return auth.ErrDenied
	}
	var active int64
	if r.DB.QueryRow(`SELECT active_workshop_id FROM user_workshop_context WHERE user_id=?`, s.UserID).Scan(&active) != nil || active != s.WorkshopID {
		return auth.ErrDenied
	}
	v := (invariants.InvariantEngine{}).Evaluate(r.DB, invariants.ProposedAction{ActionType: "marketplace_query", UserID: s.UserID, WorkshopID: s.WorkshopID}, invariants.Facts{})
	if !v.Allowed {
		return auth.ErrDenied
	}
	return nil
}
func (r *Repository) Lookup(s Scope, p Provider, connection int64) (map[int64]string, map[string]int64, error) {
	names := map[int64]string{}
	mapping := map[string]int64{}
	if e := r.Authorize(s); e != nil {
		return nil, nil, e
	}
	if connection == 0 {
		return names, mapping, nil
	}
	q := `SELECT nm_id,title FROM marketplace_cards WHERE workshop_id=? AND connection_id=?`
	if p == Ozon {
		q = `SELECT product_id,name FROM marketplace_catalog WHERE workshop_id=? AND connection_id=? AND provider='OZON'`
	}
	rows, e := r.DB.Query(q, s.WorkshopID, connection)
	if e != nil {
		return nil, nil, e
	}
	for rows.Next() {
		var id int64
		var name string
		if e = rows.Scan(&id, &name); e != nil {
			rows.Close()
			return nil, nil, e
		}
		names[id] = name
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, nil, e
	}
	if p == WB {
		rows, e = r.DB.Query(`SELECT m.nm_id,m.chrt_id,m.product_id FROM marketplace_mappings m JOIN products p ON p.id=m.product_id AND p.workshop_id=m.workshop_id WHERE m.workshop_id=? AND m.connection_id=? AND m.status='active' GROUP BY m.nm_id,m.chrt_id HAVING COUNT(DISTINCT m.product_id)=1`, s.WorkshopID, connection)
		if e != nil {
			return nil, nil, e
		}
		defer rows.Close()
		for rows.Next() {
			var nm, ch, product int64
			if e = rows.Scan(&nm, &ch, &product); e != nil {
				return nil, nil, e
			}
			mapping[fmt.Sprintf("%d/%d/", nm, ch)] = product
		}
	} else {
		rows, e = r.DB.Query(`SELECT m.sku,m.product_id FROM ozon_product_mappings m JOIN products p ON p.id=m.product_id AND p.workshop_id=m.workshop_id WHERE m.workshop_id=? AND m.connection_id=?`, s.WorkshopID, connection)
		if e != nil {
			return nil, nil, e
		}
		defer rows.Close()
		for rows.Next() {
			var sku, product int64
			if e = rows.Scan(&sku, &product); e != nil {
				return nil, nil, e
			}
			mapping[fmt.Sprint(sku)] = product
		}
	}
	return names, mapping, rows.Err()
}
func (r *Repository) MapOzon(s Scope, sku, product int64) error {
	if e := r.Authorize(s); e != nil {
		return e
	}
	if auth.Require(r.DB, s.UserID, s.WorkshopID, auth.MarketplaceManage) != nil {
		return auth.ErrDenied
	}
	var n int
	if r.DB.QueryRow(`SELECT COUNT(*) FROM marketplace_catalog_skus s JOIN marketplace_connections c ON c.id=s.connection_id AND c.workshop_id=s.workshop_id WHERE c.provider='OZON' AND c.enabled=1 AND s.workshop_id=? AND s.connection_id=2 AND s.sku=?`, s.WorkshopID, sku).Scan(&n) != nil || n != 1 {
		return auth.ErrDenied
	}
	tx, e := r.DB.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if auth.Require(tx, s.UserID, s.WorkshopID, auth.MarketplaceManage) != nil {
		return auth.ErrDenied
	}
	var active int64
	if tx.QueryRow(`SELECT active_workshop_id FROM user_workshop_context WHERE user_id=?`, s.UserID).Scan(&active) != nil || active != s.WorkshopID {
		return auth.ErrDenied
	}
	if tx.QueryRow(`SELECT COUNT(*) FROM marketplace_connections WHERE id=2 AND workshop_id=? AND enabled=1 AND provider='OZON'`, s.WorkshopID).Scan(&n) != nil || n != 1 {
		return auth.ErrDenied
	}
	if _, e = tx.Exec(`INSERT INTO ozon_product_mappings(connection_id,workshop_id,sku,product_id) VALUES(2,?,?,?) ON CONFLICT(connection_id,sku) DO UPDATE SET product_id=excluded.product_id`, s.WorkshopID, sku, product); e != nil {
		return e
	}
	if _, e = tx.Exec(`INSERT INTO audit_logs(workshop_id,entity_type,entity_id,actor_user_id,actor_name,action,details,event_type,metadata_json) VALUES(?,'marketplace',2,?,'','OZON_MAP_PRODUCT','success','OZON_MAP_PRODUCT','{}')`, s.WorkshopID, s.UserID); e != nil {
		return e
	}
	return tx.Commit()
}
func (r *Repository) LastTrace(s Scope) (Trace, error) {
	var t Trace
	if e := r.Authorize(s); e != nil {
		return t, e
	}
	if auth.Require(r.DB, s.UserID, s.WorkshopID, auth.MarketplaceManage) != nil {
		return t, auth.ErrDenied
	}
	var raw string
	e := r.DB.QueryRow(`SELECT trace_json FROM marketplace_query_traces WHERE user_id=? AND workshop_id=? ORDER BY id DESC LIMIT 1`, s.UserID, s.WorkshopID).Scan(&raw)
	if e != nil {
		return t, e
	}
	e = json.Unmarshal([]byte(raw), &t)
	return t, e
}
