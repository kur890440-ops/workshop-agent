package marketplace

import (
	"database/sql"
	"errors"
	"time"
	wb "workshop-agent/internal/marketplace/wildberries"
)

type cooldownDB struct{ db *sql.DB }

func (d cooldownDB) LoadCooldown(group string) (wb.RateLimitError, error) {
	v := wb.RateLimitError{Operation: group}
	var ms int64
	if !wb.ValidRateGroup(group) {
		return v, ErrInput
	}
	err := d.db.QueryRow(`SELECT retry_at_ms,source FROM marketplace_cooldowns WHERE rate_group=?`, group).Scan(&ms, &v.Source)
	if errors.Is(err, sql.ErrNoRows) {
		return v, nil
	}
	if err != nil {
		return v, ErrStorage
	}
	if !wb.ValidRateSource(v.Source) {
		return v, ErrStorage
	}
	v.RetryAt = time.UnixMilli(ms)
	return v, nil
}
func (d cooldownDB) SaveCooldown(v wb.RateLimitError) error {
	if !wb.ValidRateGroup(v.Operation) || !wb.ValidRateSource(v.Source) {
		return ErrInput
	}
	ms := v.RetryAt.UnixMilli()
	if time.UnixMilli(ms).Before(v.RetryAt) {
		ms++
	} // Never round the server deadline down.
	_, err := d.db.Exec(`INSERT INTO marketplace_cooldowns(rate_group,retry_at_ms,source) VALUES(?,?,?) ON CONFLICT(rate_group) DO UPDATE SET retry_at_ms=excluded.retry_at_ms,source=excluded.source WHERE excluded.retry_at_ms>=marketplace_cooldowns.retry_at_ms`, v.Operation, ms, v.Source)
	if err != nil {
		return ErrStorage
	}
	return nil
}

type identityCache struct {
	connection, workshop, revision int64
	seller                         string
	until                          time.Time
}

func (s *Service) identityValid(c Connection) bool {
	return s.identityCache.connection == c.ID && s.identityCache.workshop == c.WorkshopID && s.identityCache.revision == c.Revision && s.identityCache.seller == c.SellerID && c.Enabled && c.SellerID != "" && s.clock().Before(s.identityCache.until)
}

type dependencyError struct{ error }

func (e dependencyError) Unwrap() error { return e.error }
