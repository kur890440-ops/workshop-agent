package ozon

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/singleflight"
	"workshop-agent/internal/auth"
)

type Access struct{ UserID, WorkshopID, ConnectionID int64 }
type accessKey struct{}
type Connection struct {
	ID, WorkshopID, Revision int64
	Enabled, Configured      bool
}
type Service struct {
	db               *sql.DB
	clientID, apiKey string
	mu               sync.Mutex
	client           *Client
	clients          []*Client
	flights          singleflight.Group
}

func NewService(db *sql.DB, clientID, apiKey string) *Service {
	return &Service{db: db, clientID: clientID, apiKey: apiKey}
}
func (s *Service) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.clients {
		c.Close()
	}
}
func (s *Service) SensitiveInput(text string) bool {
	upper := strings.ToUpper(text)
	return strings.Contains(upper, "OZON_API_KEY") || strings.Contains(upper, "OZON_CLIENT_ID") || strings.Contains(upper, "API-KEY:") || s != nil && ((s.apiKey != "" && strings.Contains(text, s.apiKey)) || (s.clientID != "" && strings.Contains(text, s.clientID)))
}
func authorized(q auth.Querier, a Access, p auth.Permission) error {
	if auth.Require(q, a.UserID, a.WorkshopID, p) != nil {
		return auth.ErrDenied
	}
	var w int64
	if q.QueryRow(`SELECT active_workshop_id FROM user_workshop_context WHERE user_id=?`, a.UserID).Scan(&w) != nil || w != a.WorkshopID {
		return auth.ErrDenied
	}
	return nil
}
func resolved(q auth.Querier, a Access, enabled bool) (Connection, error) {
	var c Connection
	if a.ConnectionID != 2 {
		return c, auth.ErrDenied
	}
	if q.QueryRow(`SELECT id,workshop_id,revision,enabled FROM marketplace_connections WHERE id=? AND workshop_id=? AND provider='OZON'`, a.ConnectionID, a.WorkshopID).Scan(&c.ID, &c.WorkshopID, &c.Revision, &c.Enabled) != nil {
		return c, auth.ErrDenied
	}
	if enabled && !c.Enabled {
		return c, &Error{Code: PermissionDenied}
	}
	return c, nil
}
func (s *Service) Status(a Access) (Connection, error) {
	var c Connection
	if s == nil {
		return c, &Error{Code: NotConfigured}
	}
	if e := authorized(s.db, a, auth.MarketplaceRead); e != nil {
		return c, e
	}
	var w int64
	err := s.db.QueryRow(`SELECT workshop_id FROM marketplace_connections WHERE id=2 AND provider='OZON'`).Scan(&w)
	if errors.Is(err, sql.ErrNoRows) {
		return Connection{Configured: s.clientID != "" && s.apiKey != ""}, nil
	}
	if err != nil {
		return c, &Error{Code: StorageFailed}
	}
	if w != a.WorkshopID {
		return c, auth.ErrDenied
	}
	a.ConnectionID = 2
	c, err = resolved(s.db, a, false)
	c.Configured = s.clientID != "" && s.apiKey != ""
	return c, err
}
func auditOzon(tx *sql.Tx, a Access, action, result string) error {
	_, e := tx.Exec(`INSERT INTO audit_logs(workshop_id,entity_type,entity_id,actor_user_id,actor_name,action,details,event_type,metadata_json) VALUES(?,'marketplace',2,?,'',?,?,?,'{}')`, a.WorkshopID, a.UserID, action, result, action)
	return e
}
func (s *Service) Attach(a Access) error {
	if s.clientID == "" || s.apiKey == "" {
		return &Error{Code: NotConfigured}
	}
	tx, e := s.db.Begin()
	if e != nil {
		return &Error{Code: StorageFailed}
	}
	defer tx.Rollback()
	if e = authorized(tx, a, auth.MarketplaceManage); e != nil {
		return e
	}
	var owner int64
	e = tx.QueryRow(`SELECT workshop_id FROM marketplace_connections WHERE id=2`).Scan(&owner)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return &Error{Code: StorageFailed}
	}
	if e == nil && owner != a.WorkshopID {
		return auth.ErrDenied
	}
	fingerprint := sha256.Sum256([]byte(s.clientID))
	binding := hex.EncodeToString(fingerprint[:])
	var old string
	if e == nil {
		if tx.QueryRow(`SELECT identity_fingerprint FROM marketplace_connections WHERE id=2`).Scan(&old) != nil || old != binding {
			return &Error{Code: PermissionDenied}
		}
	}
	_, e = tx.Exec(`INSERT INTO marketplace_connections(id,workshop_id,provider,secret_ref,enabled,identity_fingerprint) VALUES(2,?,'OZON','OZON_ENV',1,?) ON CONFLICT(id) DO UPDATE SET enabled=1,revision=revision+CASE WHEN enabled=0 THEN 1 ELSE 0 END,disabled_at=''`, a.WorkshopID, binding)
	if e != nil {
		return &Error{Code: StorageFailed}
	}
	if auditOzon(tx, a, "ozon.attach", "success") != nil || tx.Commit() != nil {
		return &Error{Code: StorageFailed}
	}
	return nil
}
func (s *Service) Disable(a Access) error {
	tx, e := s.db.Begin()
	if e != nil {
		return &Error{Code: StorageFailed}
	}
	defer tx.Rollback()
	if e = authorized(tx, a, auth.MarketplaceManage); e != nil {
		return e
	}
	if _, e = resolved(tx, a, false); e != nil {
		return e
	}
	if _, e = tx.Exec(`UPDATE marketplace_connections SET enabled=0,revision=revision+1,disabled_at=CURRENT_TIMESTAMP WHERE id=2 AND workshop_id=? AND provider='OZON'`, a.WorkshopID); e != nil {
		return &Error{Code: StorageFailed}
	}
	if auditOzon(tx, a, "ozon.disable", "success") != nil || tx.Commit() != nil {
		return &Error{Code: StorageFailed}
	}
	return nil
}
func (s *Service) guard(ctx context.Context, sc Scope) error {
	a, ok := ctx.Value(accessKey{}).(Access)
	if !ok || a.ConnectionID != sc.ConnectionID || a.WorkshopID != sc.WorkshopID || ctx.Err() != nil {
		return auth.ErrDenied
	}
	if e := authorized(s.db, a, auth.MarketplaceRead); e != nil {
		return e
	}
	c, e := resolved(s.db, a, true)
	if e != nil {
		return e
	}
	if c.Revision != sc.Revision {
		return auth.ErrDenied
	}
	return nil
}
func (s *Service) bound(a Access, c Connection) (*Client, error) {
	fingerprint := sha256.Sum256([]byte(s.clientID))
	var binding string
	if s.db.QueryRow(`SELECT identity_fingerprint FROM marketplace_connections WHERE id=2 AND provider='OZON'`).Scan(&binding) != nil || binding != hex.EncodeToString(fingerprint[:]) {
		return nil, &Error{Code: PermissionDenied}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client == nil || s.client.scope.Revision != c.Revision || s.client.scope.WorkshopID != a.WorkshopID {
		s.client = New(s.clientID, s.apiKey, Scope{a.WorkshopID, a.ConnectionID, c.Revision}, sqliteState{s.db}, s.guard)
		s.clients = append(s.clients, s.client)
	}
	return s.client, nil
}

// Execute is invoked by the trusted MCP handler with the one-use grant's scope.
func (s *Service) Execute(ctx context.Context, a Access, source string, in Input) (Result, error) {
	if !validSource(source) || in.Offset < 0 || in.Offset > 100000 || in.Refresh && in.Offset != 0 {
		return Result{}, &Error{Code: InvalidInput}
	}
	if e := authorized(s.db, a, auth.MarketplaceRead); e != nil {
		return Result{}, e
	}
	c, e := resolved(s.db, a, in.Refresh)
	if e != nil {
		return Result{}, e
	}
	if !in.Refresh {
		r, e := s.cached(a, source, in.Offset)
		if e != nil {
			return r, e
		}
		if s.logicalTrace(ctx, a, r, "CACHE") != nil {
			return Result{}, &Error{Code: StorageFailed}
		}
		return r, nil
	}
	client, e := s.bound(a, c)
	if e != nil {
		return Result{}, e
	}
	ctx = context.WithValue(ctx, accessKey{}, a)
	key := fmt.Sprintf("%d/%d/%d/%s", a.WorkshopID, a.ConnectionID, c.Revision, source)
	var executed atomic.Bool
	ch := s.flights.DoChan(key, func() (any, error) {
		executed.Store(true)
		skus, e := s.skus(a)
		if e != nil {
			return Result{}, e
		}
		r := client.Fetch(ctx, source, skus)
		if source == SellerSource {
			var count, missing int
			if e = s.db.QueryRow(`SELECT COUNT(*),COALESCE(SUM(CASE WHEN NOT EXISTS(SELECT 1 FROM marketplace_catalog_skus k WHERE k.connection_id=p.connection_id AND k.workshop_id=p.workshop_id AND k.product_id=p.product_id AND k.sku>0) THEN 1 ELSE 0 END),0) FROM marketplace_catalog p WHERE p.connection_id=? AND p.workshop_id=? AND p.provider='OZON'`, a.ConnectionID, a.WorkshopID).Scan(&count, &missing); e != nil {
				return Result{}, &Error{Code: StorageFailed}
			}
			r.Metrics.ProductsConsidered = count
			r.Metrics.ProductsMissingSKU = missing
			if missing > 0 && r.Status == Success {
				r.Status = Partial
				r.ErrorCode = "CATALOG_SKUS_MISSING"
			}
		}
		if e = s.guard(ctx, client.scope); e != nil {
			return Result{}, e
		}
		if e = s.save(ctx, a, c.Revision, &r); e != nil {
			return Result{}, e
		}
		out, e := s.cached(a, source, 0)
		if e != nil {
			return Result{}, e
		}
		out.Metrics = r.Metrics
		out.Cache = "BYPASS"
		out.Dedup = "NEW"
		if s.logicalTrace(ctx, a, out, "SYNC") != nil {
			return Result{}, &Error{Code: StorageFailed}
		}
		return out, nil
	})
	select {
	case <-ctx.Done():
		return Result{}, &Error{Code: Failed}
	case v := <-ch:
		if e = s.guard(ctx, client.scope); e != nil {
			return Result{}, e
		}
		if v.Err != nil {
			return Result{}, v.Err
		}
		r := v.Val.(Result)
		r.Cache = "BYPASS"
		r.Dedup = "NEW"
		if !executed.Load() {
			r.Dedup = "JOINED_EXISTING"
			r.Metrics.HTTPRequests = 0
			r.Metrics.Pages = 0
			r.Metrics.Saved = 0
			if s.logicalTrace(ctx, a, r, "DEDUP") != nil {
				return Result{}, &Error{Code: StorageFailed}
			}
		}
		return r, nil
	}
}
func (s *Service) skus(a Access) ([]int64, error) {
	rows, e := s.db.Query(`SELECT sku FROM marketplace_catalog_skus WHERE connection_id=? AND workshop_id=? AND provider='OZON' ORDER BY sku LIMIT 10001`, a.ConnectionID, a.WorkshopID)
	if e != nil {
		return nil, &Error{Code: StorageFailed}
	}
	defer rows.Close()
	out := []int64{}
	for rows.Next() {
		var id int64
		if rows.Scan(&id) != nil {
			return nil, &Error{Code: StorageFailed}
		}
		out = append(out, id)
	}
	if rows.Err() != nil {
		return nil, &Error{Code: StorageFailed}
	}
	if len(out) > 10000 {
		return nil, &Error{Code: InvalidInput}
	}
	return out, nil
}

type sqliteState struct{ db *sql.DB }

func (d sqliteState) LoadCooldown(ctx context.Context, sc Scope) (time.Time, error) {
	var stamp string
	e := d.db.QueryRowContext(ctx, `SELECT retry_at FROM integration_cooldowns WHERE connection_id=? AND workshop_id=? AND provider='OZON'`, sc.ConnectionID, sc.WorkshopID).Scan(&stamp)
	if errors.Is(e, sql.ErrNoRows) {
		return time.Time{}, nil
	}
	if e != nil {
		return time.Time{}, e
	}
	return time.Parse(time.RFC3339Nano, stamp)
}
func (d sqliteState) SaveCooldown(ctx context.Context, sc Scope, until time.Time) error {
	// This client serializes requests; persistent deadline is also monotonic.
	old, e := d.LoadCooldown(ctx, sc)
	if e != nil {
		return e
	}
	if old.After(until) {
		until = old
	}
	_, e = d.db.ExecContext(ctx, `INSERT INTO integration_cooldowns(connection_id,workshop_id,provider,retry_at) VALUES(?,?,'OZON',?) ON CONFLICT(connection_id,provider) DO UPDATE SET retry_at=excluded.retry_at`, sc.ConnectionID, sc.WorkshopID, until.UTC().Format(time.RFC3339Nano))
	return e
}
func (d sqliteState) AppendTrace(ctx context.Context, t Trace) error {
	b, e := json.Marshal(t)
	if e != nil {
		return e
	}
	_, e = d.db.ExecContext(ctx, `INSERT INTO integration_request_trace(connection_id,workshop_id,provider,timestamp,trace_json) VALUES(?,?,'OZON',?,?)`, t.ConnectionID, t.WorkshopID, t.Timestamp.UTC().Format(time.RFC3339Nano), string(b))
	return e
}
