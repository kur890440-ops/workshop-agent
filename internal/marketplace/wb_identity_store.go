package marketplace

import (
	"database/sql"
	"errors"
	"time"
	wb "workshop-agent/internal/marketplace/wildberries"
)

func (d cooldownDB) LoadSeller(fingerprint string) (out wb.SellerCache, err error) {
	var stamp string
	err = d.db.QueryRow(`SELECT seller_id,seller_name,checked_at FROM marketplace_connections WHERE id=1 AND enabled=1 AND identity_fingerprint=? AND seller_id<>''`, fingerprint).Scan(&out.Seller.ID, &out.Seller.Name, &stamp)
	if errors.Is(err, sql.ErrNoRows) {
		return wb.SellerCache{}, nil
	}
	if err != nil {
		return out, ErrStorage
	}
	out.FetchedAt, err = time.Parse(time.RFC3339Nano, stamp)
	if err != nil {
		return wb.SellerCache{}, ErrStorage
	}
	return
}
func (d cooldownDB) SaveSeller(fingerprint string, v wb.SellerCache) error {
	result, e := d.db.Exec(`UPDATE marketplace_connections SET seller_id=?,seller_name=?,checked_at=?,check_error='',identity_fingerprint=? WHERE id=1 AND enabled=1 AND (seller_id='' OR seller_id=?)`, v.Seller.ID, v.Seller.Name, v.FetchedAt.UTC().Format(time.RFC3339Nano), fingerprint, v.Seller.ID)
	if e != nil {
		return ErrStorage
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return ErrIdentity
	}
	return nil
}
