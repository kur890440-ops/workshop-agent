// Package stability owns process-wide admission and validated runtime settings.
package stability

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"
	"workshop-agent/internal/auth"
)

type Settings struct{ Rate, Local, Cloud, Queue, MaxContext, MaxOutput, Timeout int }

func Defaults() Settings { return Settings{20, 1, 2, 5, 16384, 2048, 120} }

var names = []string{"rate", "local_concurrency", "cloud_concurrency", "queue", "max_context", "max_output", "timeout"}

func (s Settings) Values() map[string]int {
	return map[string]int{"rate": s.Rate, "local_concurrency": s.Local, "cloud_concurrency": s.Cloud, "queue": s.Queue, "max_context": s.MaxContext, "max_output": s.MaxOutput, "timeout": s.Timeout}
}
func (s *Settings) set(k string, v int) error {
	switch k {
	case "rate":
		s.Rate = v
	case "local_concurrency":
		s.Local = v
	case "cloud_concurrency":
		s.Cloud = v
	case "queue":
		s.Queue = v
	case "max_context":
		s.MaxContext = v
	case "max_output":
		s.MaxOutput = v
	case "timeout":
		s.Timeout = v
	default:
		return errors.New("UNKNOWN_LIMIT")
	}
	return nil
}
func (s Settings) Validate() error {
	if s.Rate < 1 || s.Rate > 600 || s.Local < 1 || s.Local > 4 || s.Cloud < 1 || s.Cloud > 8 || s.Queue < 1 || s.Queue > 100 || s.MaxContext < 1024 || s.MaxContext > 16384 || s.MaxOutput < 16 || s.MaxOutput > 4096 || s.MaxOutput+512 >= s.MaxContext || s.Timeout < 5 || s.Timeout > 600 {
		return errors.New("INVALID_LIMIT: rate 1..600, local 1..4, cloud 1..8, queue 1..100, context 1024..16384, output 16..4096, timeout 5..600; output+512<context")
	}
	return nil
}

type Store struct {
	OnChange      func()
	mu            sync.RWMutex
	DB            *sql.DB
	effective     Settings
	ValidateModel func(Settings) error
}

func NewStore(db *sql.DB, base Settings) (*Store, error) {
	if e := base.Validate(); e != nil {
		return nil, e
	}
	rows, e := db.Query("SELECT parameter,value FROM runtime_limits")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		var v int
		if e = rows.Scan(&k, &v); e != nil {
			return nil, e
		}
		if e = base.set(k, v); e != nil {
			return nil, e
		}
	}
	if e = rows.Err(); e != nil {
		return nil, e
	}
	if e = base.Validate(); e != nil {
		return nil, e
	}
	return &Store{DB: db, effective: base}, nil
}
func FromEnv() (Settings, error) {
	s := Defaults()
	for _, k := range names {
		env := map[string]string{"rate": "LIMIT_RATE", "local_concurrency": "LIMIT_LOCAL_CONCURRENCY", "cloud_concurrency": "LIMIT_CLOUD_CONCURRENCY", "queue": "LIMIT_QUEUE", "max_context": "LIMIT_MAX_CONTEXT", "max_output": "LIMIT_MAX_OUTPUT", "timeout": "LIMIT_REQUEST_TIMEOUT"}[k]
		if raw := os.Getenv(env); raw != "" {
			v, e := strconv.Atoi(raw)
			if e != nil {
				return s, fmt.Errorf("invalid %s", env)
			}
			_ = s.set(k, v)
		}
	}
	return s, s.Validate()
}
func (s *Store) Get() Settings { s.mu.RLock(); defer s.mu.RUnlock(); return s.effective }
func (s *Store) Change(ctx context.Context, actor, workshop int64, key string, value int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.effective
	if e := next.set(key, value); e != nil {
		return e
	}
	if e := next.Validate(); e != nil {
		return e
	}
	if s.ValidateModel != nil && (key == "local_concurrency" || key == "max_context" || key == "max_output") {
		if e := s.ValidateModel(next); e != nil {
			return e
		}
	}
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = auth.Require(tx, actor, workshop, auth.WorkshopManage); e != nil {
		return e
	}
	old := s.effective.Values()[key]
	if _, e = tx.Exec("INSERT INTO runtime_limits(parameter,value) VALUES(?,?) ON CONFLICT(parameter) DO UPDATE SET value=excluded.value", key, value); e != nil {
		return e
	}
	if _, e = tx.Exec("INSERT INTO runtime_limit_audit(actor,workshop_id,parameter,old_value,new_value,created_at) VALUES(?,?,?,?,?,?)", actor, workshop, key, old, value, time.Now().Unix()); e != nil {
		return e
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	s.effective = next
	if s.OnChange != nil {
		go s.OnChange()
	}
	return nil
}

type Error struct {
	Code       string
	RetryAfter int
}

func (e *Error) Error() string { return e.Code }
func Code(e error) string {
	if e == nil {
		return ""
	}
	var x *Error
	if errors.As(e, &x) {
		return x.Code
	}
	if errors.Is(e, context.Canceled) {
		return "CANCELLED"
	}
	if errors.Is(e, context.DeadlineExceeded) {
		return "REQUEST_TIMEOUT"
	}
	if errors.Is(e, auth.ErrDenied) || errors.Is(e, auth.ErrDisabled) {
		return "ACCESS_DENIED"
	}
	return "APPLICATION_FAILED"
}

type contextKey struct{}
type Scope struct {
	WebSession int64
	User       int64
	Settings   Settings
	ID         string
	Yield      func() func() error
	State      func(string)
}

func WithScope(ctx context.Context, s *Scope) context.Context {
	return context.WithValue(ctx, contextKey{}, s)
}
func ScopeFrom(ctx context.Context) *Scope { s, _ := ctx.Value(contextKey{}).(*Scope); return s }
func Config(ctx context.Context) Settings {
	if s := ScopeFrom(ctx); s != nil {
		return s.Settings
	}
	return Defaults()
}
