package marketplace

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"strings"
	"sync"
	"time"

	"workshop-agent/internal/auth"
	wb "workshop-agent/internal/marketplace/wildberries"
)

type Error string

func (e Error) Error() string { return string(e) }

const (
	ErrScope    Error = "Подключение WB недоступно в этой мастерской."
	ErrDisabled Error = "Подключение WB отключено."
	ErrBusy     Error = "Обновление WB уже выполняется. Статус: /wb."
	ErrIdentity Error = "Токен относится к другому кабинету WB. Данные не обновлены."
	ErrInput    Error = "Неверные параметры WB. Справка: /wb help."
	ErrStorage  Error = "Не удалось сохранить данные WB."
)

type API interface {
	Configured() bool
	ContainsSecret(string) bool
	Seller(context.Context) (wb.Seller, error)
	Catalog(context.Context) ([]wb.Card, error)
	SellerStocks(context.Context, []wb.Card) ([]wb.Stock, error)
	WBStocks(context.Context) ([]wb.Stock, error)
	NewOrders(context.Context) ([]wb.Order, error)
	OrderStatuses(context.Context, []int64) ([]wb.Status, error)
}
type Scope struct{ UserID, WorkshopID, ConnectionID int64 }
type Connection struct {
	ID, WorkshopID, Revision                    int64
	Enabled, Configured                         bool
	SellerID, SellerName, CheckedAt, CheckError string
	Sync                                        []SyncState
}
type SyncState struct {
	Kind, State, StartedAt, FinishedAt, LastSuccess, ErrorCode string
	Rows                                                       int
	BlockedBy, RetryAt, RetrySource, RateOperation             string
}
type Service struct {
	db            *sql.DB
	api           API
	mu            sync.Mutex
	job           *job
	wg            sync.WaitGroup
	identityCache identityCache
	clock         func() time.Time
}
type job struct {
	scope    Scope
	revision int64
	cancel   context.CancelFunc
	done     chan struct{}
}

// New must be called once per application process, before accepting commands.
func New(db *sql.DB, api API) (*Service, error) {
	if db == nil || api == nil {
		return nil, ErrInput
	}
	s := &Service{db: db, api: api, clock: time.Now}
	if c, ok := api.(interface{ SetCooldownStore(wb.CooldownStore) }); ok {
		c.SetCooldownStore(cooldownDB{db})
	}
	_, err := db.Exec(`UPDATE marketplace_sync SET state='cancelled',error_code='interrupted',finished_at=CURRENT_TIMESTAMP WHERE state='running'`)
	if err != nil {
		return nil, ErrStorage
	}
	return s, nil
}
func (s *Service) Close() {
	s.mu.Lock()
	if s.job != nil {
		s.job.cancel()
	}
	s.mu.Unlock()
	s.wg.Wait()
}

var credentialPattern = regexp.MustCompile(`(?i)(WB_API_TOKEN\s*=|\beyJ[A-Za-z0-9_-]{12,}\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+)`)

func (s *Service) SensitiveInput(text string) bool {
	return credentialPattern.MatchString(text) || s != nil && s.api.ContainsSecret(text)
}
func authorize(q auth.Querier, scope Scope, p auth.Permission) error {
	if err := auth.Require(q, scope.UserID, scope.WorkshopID, p); err != nil {
		return err
	}
	var active int64
	if q.QueryRow(`SELECT active_workshop_id FROM user_workshop_context WHERE user_id=?`, scope.UserID).Scan(&active) != nil || active != scope.WorkshopID {
		return ErrScope
	}
	return nil
}
func connection(q auth.Querier, scope Scope, enabled bool) (Connection, error) {
	var c Connection
	if scope.ConnectionID <= 0 {
		return c, ErrScope
	}
	err := q.QueryRow(`SELECT id,workshop_id,revision,enabled,seller_id,seller_name,checked_at,check_error FROM marketplace_connections WHERE id=? AND workshop_id=?`, scope.ConnectionID, scope.WorkshopID).Scan(&c.ID, &c.WorkshopID, &c.Revision, &c.Enabled, &c.SellerID, &c.SellerName, &c.CheckedAt, &c.CheckError)
	if err != nil {
		return c, ErrScope
	}
	if enabled && !c.Enabled {
		return c, ErrDisabled
	}
	return c, nil
}
func (s *Service) Status(scope Scope) (Connection, error) {
	var c Connection
	if err := authorize(s.db, scope, auth.MarketplaceRead); err != nil {
		return c, err
	}
	if scope.ConnectionID == 0 {
		var id int64
		err := s.db.QueryRow(`SELECT id FROM marketplace_connections WHERE workshop_id=?`, scope.WorkshopID).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			return Connection{Configured: s.api.Configured()}, nil
		}
		if err != nil {
			return c, ErrStorage
		}
		scope.ConnectionID = id
	}
	c, err := connection(s.db, scope, false)
	if err != nil {
		return c, err
	}
	c.Configured = s.api.Configured()
	rows, err := s.db.Query(`SELECT kind,state,started_at,finished_at,last_success,error_code,row_count,blocked_by,retry_at,retry_source,rate_operation FROM marketplace_sync WHERE connection_id=? AND workshop_id=? ORDER BY kind`, c.ID, scope.WorkshopID)
	if err != nil {
		return c, ErrStorage
	}
	defer rows.Close()
	for rows.Next() {
		var r SyncState
		if rows.Scan(&r.Kind, &r.State, &r.StartedAt, &r.FinishedAt, &r.LastSuccess, &r.ErrorCode, &r.Rows, &r.BlockedBy, &r.RetryAt, &r.RetrySource, &r.RateOperation) != nil {
			return c, ErrStorage
		}
		c.Sync = append(c.Sync, r)
	}
	if rows.Err() != nil {
		return c, ErrStorage
	}
	return c, nil
}
func audit(tx *sql.Tx, scope Scope, action, result string) error {
	_, err := tx.Exec(`INSERT INTO audit_logs(workshop_id,entity_type,entity_id,actor_user_id,actor_name,action,details,event_type,metadata_json) VALUES(?,'marketplace',?,?,'',?,?,?,'{}')`, scope.WorkshopID, scope.ConnectionID, scope.UserID, action, result, action)
	return err
}
func (s *Service) Attach(scope Scope) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return 0, ErrStorage
	}
	defer tx.Rollback()
	if err := authorize(tx, scope, auth.MarketplaceManage); err != nil {
		return 0, err
	}
	if !s.api.Configured() {
		return 0, wb.NotConfigured
	}
	var workshop int64
	err = tx.QueryRow(`SELECT workshop_id FROM marketplace_connections WHERE id=1`).Scan(&workshop)
	if err == nil && workshop != scope.WorkshopID {
		return 0, ErrScope
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, ErrStorage
	}
	scope.ConnectionID = 1
	if _, err = tx.Exec(`INSERT INTO marketplace_connections(id,workshop_id,enabled) VALUES(1,?,1) ON CONFLICT(id) DO UPDATE SET enabled=1,revision=revision+CASE WHEN enabled=0 THEN 1 ELSE 0 END,disabled_at=''`, scope.WorkshopID); err != nil {
		return 0, ErrStorage
	}
	if audit(tx, scope, "WB_ATTACH", "enabled") != nil || tx.Commit() != nil {
		return 0, ErrStorage
	}
	return 1, nil
}
func (s *Service) Disable(scope Scope) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return ErrStorage
	}
	defer tx.Rollback()
	if err := authorize(tx, scope, auth.MarketplaceManage); err != nil {
		return err
	}
	if _, err := connection(tx, scope, false); err != nil {
		return err
	}
	if _, err = tx.Exec(`UPDATE marketplace_connections SET enabled=0,revision=revision+1,disabled_at=CURRENT_TIMESTAMP WHERE id=? AND workshop_id=?`, scope.ConnectionID, scope.WorkshopID); err != nil {
		return ErrStorage
	}
	if audit(tx, scope, "WB_DISABLE", "disabled") != nil || tx.Commit() != nil {
		return ErrStorage
	}
	if s.job != nil {
		s.job.cancel()
	}
	return nil
}
func (s *Service) check(scope Scope, revision int64) error {
	if err := authorize(s.db, scope, auth.MarketplaceRead); err != nil {
		return err
	}
	c, err := connection(s.db, scope, true)
	if err != nil {
		return err
	}
	if c.Revision != revision {
		return ErrDisabled
	}
	return nil
}
func validKind(kind string) bool {
	switch kind {
	case "check", "all", "catalog", "seller_stocks", "wb_stocks", "orders":
		return true
	}
	return false
}

// Start returns immediately. The done channel is for shutdown/tests; Telegram polls Status.
func (s *Service) Start(scope Scope, kind string) (<-chan struct{}, error) {
	if !validKind(kind) {
		return nil, ErrInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := authorize(s.db, scope, auth.MarketplaceRead); err != nil {
		return nil, err
	}
	c, err := connection(s.db, scope, true)
	if err != nil {
		return nil, err
	}
	if !s.api.Configured() {
		return nil, wb.NotConfigured
	}
	if s.job != nil {
		return nil, ErrBusy
	}
	needIdentity := kind == "check" || !s.identityValid(c)
	// Refuse known cooldowns before starting another job/notification.
	groups := []string{}
	if needIdentity {
		groups = append(groups, "common")
	}
	if kind == "wb_stocks" || kind == "all" {
		groups = append(groups, "analytics")
	}
	for _, group := range groups {
		v, e := (cooldownDB{s.db}).LoadCooldown(group)
		if e != nil {
			return nil, e
		}
		if v.RetryAt.After(s.clock()) {
			return nil, &v
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	j := &job{scope: scope, revision: c.Revision, cancel: cancel, done: make(chan struct{})}
	s.job = j
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer cancel()
		defer func() { s.mu.Lock(); s.job = nil; close(j.done); s.mu.Unlock() }()
		ctx = wb.WithGuard(ctx, func() error { return s.check(scope, j.revision) })
		kinds := []string{kind}
		if kind == "all" {
			kinds = []string{"catalog", "seller_stocks", "wb_stocks", "orders"}
		}
		// Identity is checked on every job. Local token rotation cannot silently switch seller.
		var err error
		if needIdentity {
			err = s.identity(ctx, j)
		}
		if kind == "check" {
			return
		}
		for _, k := range kinds {
			if e := s.beginRun(j, k); e != nil {
				return
			}
			if err != nil {
				_ = s.finish(j, k, 0, dependencyError{err}, nil)
				continue
			}
			if e := s.check(scope, j.revision); e != nil {
				_ = s.finish(j, k, 0, e, nil)
				return
			}
			s.sync(ctx, j, k)
		}
	}()
	return j.done, nil
}
func safeCode(err error) string {
	if err == nil {
		return ""
	}
	var w wb.Error
	if errors.As(err, &w) {
		switch w {
		case wb.NotConfigured, wb.InvalidInput, wb.InvalidResponse, wb.Unauthorized, wb.Forbidden, wb.RateLimited, wb.Unavailable, wb.Timeout, wb.Cancelled, wb.ResponseTooLarge, wb.PageLimit, wb.RedirectDenied:
			return string(w)
		}
	}
	switch {
	case errors.Is(err, ErrIdentity):
		return "seller_changed"
	case errors.Is(err, ErrDisabled), errors.Is(err, ErrScope), errors.Is(err, auth.ErrDenied), errors.Is(err, auth.ErrDisabled), errors.Is(err, context.Canceled):
		return "cancelled"
	}
	return "storage_error"
}
func (s *Service) beginRun(j *job, kind string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return ErrStorage
	}
	defer tx.Rollback()
	if err := authorize(tx, j.scope, auth.MarketplaceRead); err != nil {
		return err
	}
	c, err := connection(tx, j.scope, true)
	if err != nil {
		return err
	}
	if c.Revision != j.revision {
		return ErrDisabled
	}
	_, err = tx.Exec(`INSERT INTO marketplace_sync(connection_id,workshop_id,kind,state,started_at) VALUES(?,?,?,'running',?) ON CONFLICT(connection_id,kind) DO UPDATE SET state='running',started_at=excluded.started_at,finished_at='',error_code='',row_count=0,blocked_by='',retry_at='',retry_source='',rate_operation=''`, j.scope.ConnectionID, j.scope.WorkshopID, kind, now())
	if err != nil || audit(tx, j.scope, "WB_SYNC_START", kind) != nil || tx.Commit() != nil {
		return ErrStorage
	}
	return nil
}
func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }
func (s *Service) finish(j *job, kind string, count int, runErr error, save func(*sql.Tx, string) error) error {
	tx, err := s.db.Begin()
	if err != nil {
		return ErrStorage
	}
	defer tx.Rollback()
	c, scopeErr := connection(tx, j.scope, true)
	if scopeErr == nil {
		scopeErr = authorize(tx, j.scope, auth.MarketplaceRead)
	}
	if scopeErr == nil && c.Revision != j.revision {
		scopeErr = ErrDisabled
	}
	if scopeErr != nil {
		runErr = scopeErr
		save = nil
	}
	stamp := now()
	state := "succeeded"
	code := safeCode(runErr)
	if runErr != nil {
		state = "failed"
		if count > 0 {
			state = "partial"
		}
		if code == "cancelled" {
			state = "cancelled"
		}
	}
	if runErr == nil && save != nil {
		if err = save(tx, stamp); err != nil {
			return ErrStorage
		}
	}
	blocked, retryAt, retrySource, operation := "", "", "", ""
	var dep dependencyError
	if errors.As(runErr, &dep) {
		blocked = "check"
	}
	var rate *wb.RateLimitError
	if errors.As(runErr, &rate) && wb.ValidRateGroup(rate.Operation) && wb.ValidRateSource(rate.Source) {
		retryAt = rate.RetryAt.UTC().Format(time.RFC3339Nano)
		retrySource = rate.Source
		operation = rate.Operation
	}
	_, err = tx.Exec(`UPDATE marketplace_sync SET state=?,finished_at=?,error_code=?,row_count=?,last_success=CASE WHEN ?='succeeded' THEN ? ELSE last_success END,blocked_by=?,retry_at=?,retry_source=?,rate_operation=? WHERE connection_id=? AND workshop_id=? AND kind=?`, state, stamp, code, count, state, stamp, blocked, retryAt, retrySource, operation, j.scope.ConnectionID, j.scope.WorkshopID, kind)
	if err != nil || audit(tx, j.scope, "WB_SYNC_FINISH", kind+":"+state+":"+code) != nil || tx.Commit() != nil {
		return ErrStorage
	}
	if errors.Is(runErr, wb.Unauthorized) || errors.Is(runErr, wb.Forbidden) || errors.Is(runErr, ErrIdentity) {
		s.mu.Lock()
		s.identityCache = identityCache{}
		s.mu.Unlock()
	}
	return runErr
}
func (s *Service) identity(ctx context.Context, j *job) error {
	s.mu.Lock()
	s.identityCache = identityCache{}
	s.mu.Unlock()
	if err := s.beginRun(j, "check"); err != nil {
		return err
	}
	seller, err := s.api.Seller(ctx)
	if err == nil {
		c, e := connection(s.db, j.scope, true)
		if e != nil {
			err = e
		} else if c.SellerID != "" && c.SellerID != seller.ID {
			err = ErrIdentity
		}
	}
	save := func(tx *sql.Tx, stamp string) error {
		_, e := tx.Exec(`UPDATE marketplace_connections SET seller_id=?,seller_name=?,checked_at=?,check_error='' WHERE id=? AND workshop_id=?`, seller.ID, seller.Name, stamp, j.scope.ConnectionID, j.scope.WorkshopID)
		return e
	}
	result := s.finish(j, "check", 0, err, save)
	if result == ErrStorage {
		_ = s.finish(j, "check", 0, result, nil)
	}
	if result != nil {
		_, _ = s.db.Exec(`UPDATE marketplace_connections SET check_error=? WHERE id=? AND workshop_id=? AND revision=?`, safeCode(result), j.scope.ConnectionID, j.scope.WorkshopID, j.revision)
	}
	if result == nil {
		s.mu.Lock()
		s.identityCache = identityCache{j.scope.ConnectionID, j.scope.WorkshopID, j.revision, seller.ID, s.clock().Add(24 * time.Hour)}
		s.mu.Unlock()
	}
	return result
}

func (s *Service) SetMapping(scope Scope, product, nm, chrt int64, barcode string) error {
	if product <= 0 || nm <= 0 || chrt <= 0 || len(barcode) > 100 || strings.ContainsAny(barcode, "\r\n\x00") || s.SensitiveInput(barcode) {
		return ErrInput
	}
	tx, err := s.db.Begin()
	if err != nil {
		return ErrStorage
	}
	defer tx.Rollback()
	if err := authorize(tx, scope, auth.MarketplaceManage); err != nil {
		return err
	}
	if _, err := connection(tx, scope, true); err != nil {
		return err
	}
	var n int
	if tx.QueryRow(`SELECT COUNT(*) FROM products p JOIN marketplace_variants v ON v.workshop_id=p.workshop_id WHERE p.id=? AND p.workshop_id=? AND v.connection_id=? AND v.nm_id=? AND v.chrt_id=? AND v.present=1`, product, scope.WorkshopID, scope.ConnectionID, nm, chrt).Scan(&n) != nil || n != 1 {
		return ErrInput
	}
	if barcode != "" {
		if tx.QueryRow(`SELECT COUNT(*) FROM marketplace_barcodes WHERE connection_id=? AND chrt_id=? AND barcode=?`, scope.ConnectionID, chrt, barcode).Scan(&n) != nil || n != 1 {
			return ErrInput
		}
	}
	if tx.QueryRow(`SELECT COUNT(*) FROM marketplace_mappings WHERE connection_id=? AND chrt_id=? AND barcode<>? AND (barcode='' OR ?='')`, scope.ConnectionID, chrt, barcode, barcode).Scan(&n) != nil || n != 0 {
		return ErrInput
	}
	_, err = tx.Exec(`INSERT INTO marketplace_mappings(connection_id,workshop_id,product_id,nm_id,chrt_id,barcode) VALUES(?,?,?,?,?,?) ON CONFLICT(connection_id,chrt_id,barcode) DO UPDATE SET product_id=excluded.product_id,status='active'`, scope.ConnectionID, scope.WorkshopID, product, nm, chrt, barcode)
	if err != nil || audit(tx, scope, "WB_MAPPING", "updated") != nil || tx.Commit() != nil {
		return ErrStorage
	}
	return nil
}
