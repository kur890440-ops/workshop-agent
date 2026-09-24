// Package background owns persistent jobs; executors own integration-specific work.
package background

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"
	"workshop-agent/internal/auth"
)

type Error string

func (e Error) Error() string { return string(e) }

const ErrInput Error = "Неверные параметры фонового задания."
const ErrBusy Error = "Загрузка за эту дату уже запускалась или ещё выполняется."
const ErrScope Error = "Задание или подключение недоступно."

type Sender interface {
	Send(context.Context, int64, int64, string) error
}
type Job struct {
	ID, WorkshopID, CreatedBy                               int64
	Type, Status, Schedule, LocalTime, Timezone, Parameters string
	NotificationEnabled                                     bool
	CreatedAt, UpdatedAt                                    int64
	NextRun, LastRun, LastSuccess                           int64
	LastResult                                              string
}
type CreateInput struct {
	Type, LocalTime, Timezone, Parameters string
	NotificationEnabled                   bool
	LowStockThreshold                     *int
}
type Service struct {
	*Repository
	Registry  *Registry
	Scheduler *Scheduler
	WB        *WBDailySyncExecutor
	Sender    Sender
	Now       func() time.Time
}

// New constructs one registry and one scheduler; neither starts work here.
func New(db *sql.DB, client MCP, sender Sender) *Service {
	s := &Service{Repository: &Repository{DB: db}, Registry: NewRegistry(), Sender: sender, Now: time.Now}
	s.WB = &WBDailySyncExecutor{DB: db, MCP: client, Now: func() time.Time { return s.Now() }}
	_ = s.Registry.Register(JobType, s.WB)
	s.Scheduler = &Scheduler{Service: s}
	return s
}
func strictJSON(raw string, out any) error {
	d := json.NewDecoder(strings.NewReader(raw))
	d.DisallowUnknownFields()
	if e := d.Decode(out); e != nil {
		return ErrInput
	}
	if d.Decode(new(any)) != io.EOF {
		return ErrInput
	}
	return nil
}
func active(q auth.Querier, user, workshop int64) error {
	var id int64
	if q.QueryRow(`SELECT active_workshop_id FROM user_workshop_context WHERE user_id=?`, user).Scan(&id) != nil || id != workshop {
		return ErrScope
	}
	return nil
}
func (s *Service) CreateJob(user, workshop int64, in CreateInput) (Job, error) {
	ex := s.Registry.Lookup(in.Type)
	if ex == nil {
		return Job{}, ErrInput
	}
	_, manage := ex.Permissions()
	if in.LocalTime == "" {
		in.LocalTime = "08:00"
	}
	if in.Parameters == "" {
		in.Parameters = "{}"
	}
	if len(in.Parameters) > 4096 {
		return Job{}, ErrInput
	}
	tx, e := s.DB.Begin()
	if e != nil {
		return Job{}, e
	}
	defer tx.Rollback()
	if e = auth.Require(tx, user, workshop, manage); e != nil {
		return Job{}, e
	}
	if e = active(tx, user, workshop); e != nil {
		return Job{}, e
	}
	if _, e = tx.Exec(`INSERT INTO workshop_settings(workshop_id) VALUES(?) ON CONFLICT DO NOTHING`, workshop); e != nil {
		return Job{}, e
	}
	if in.Timezone == "" {
		if e = tx.QueryRow(`SELECT timezone FROM workshop_settings WHERE workshop_id=?`, workshop).Scan(&in.Timezone); e != nil {
			return Job{}, e
		}
	}
	now := s.Now()
	next, e := Next(now, in.LocalTime, in.Timezone, true)
	if e != nil {
		return Job{}, e
	}
	params, e := ex.Validate(tx, user, workshop, in.Parameters)
	if e != nil {
		return Job{}, e
	}
	if in.LowStockThreshold != nil && (in.Type != JobType || *in.LowStockThreshold < 0 || *in.LowStockThreshold > 1000000) {
		return Job{}, ErrInput
	}
	// Idempotent creation never silently changes an existing schedule or re-enables it.
	var existing int64
	e = tx.QueryRow(`SELECT id FROM background_jobs WHERE workshop_id=? AND job_type=?`, workshop, in.Type).Scan(&existing)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return Job{}, e
	}
	if existing != 0 {
		j, e := scanJob(tx.QueryRow(`SELECT `+jobColumns+` FROM background_jobs WHERE id=?`, existing))
		return j, e
	}
	_, e = tx.Exec(`UPDATE workshop_settings SET timezone=? WHERE workshop_id=?`, in.Timezone, workshop)
	if e != nil {
		return Job{}, e
	}
	if in.LowStockThreshold != nil {
		if _, e = tx.Exec(`UPDATE workshop_settings SET wb_low_stock_threshold=? WHERE workshop_id=?`, *in.LowStockThreshold, workshop); e != nil {
			return Job{}, e
		}
	}
	result, e := tx.Exec(`INSERT INTO background_jobs(workshop_id,created_by_user_id,job_type,status,schedule_type,local_time,timezone,parameters_json,notification_enabled,next_run_at,created_at,updated_at) VALUES(?,?,?,'active','DAILY_AT_TIME',?,?,?,?,?,?,?)`, workshop, user, in.Type, in.LocalTime, in.Timezone, params, in.NotificationEnabled, next.Unix(), now.Unix(), now.Unix())
	if e != nil {
		return Job{}, e
	}
	id, _ := result.LastInsertId()
	j, e := scanJob(tx.QueryRow(`SELECT `+jobColumns+` FROM background_jobs WHERE id=?`, id))
	if e != nil {
		return Job{}, e
	}
	if e = tx.Commit(); e != nil {
		return Job{}, e
	}
	return j, nil
}
func (s *Service) GetJob(user, workshop, id int64) (Job, error) {
	j, e := s.Repository.Get(workshop, id)
	if e != nil {
		return Job{}, ErrScope
	}
	ex := s.Registry.Lookup(j.Type)
	if ex == nil {
		return Job{}, ErrInput
	}
	read, _ := ex.Permissions()
	if e = auth.Require(s.DB, user, workshop, read); e != nil {
		return Job{}, e
	}
	return j, nil
}
func (s *Service) ListJobs(user, workshop int64) ([]Job, error) {
	jobs, e := s.Repository.List(workshop)
	if e != nil {
		return nil, e
	}
	out := []Job{}
	for _, j := range jobs {
		allowed, e := s.GetJob(user, workshop, j.ID)
		if e != nil {
			return nil, e
		}
		out = append(out, allowed)
	}
	// Also check membership when the list is empty.
	if len(jobs) == 0 {
		if e = auth.Require(s.DB, user, workshop, auth.MarketplaceRead); e != nil {
			return nil, e
		}
	}
	return out, nil
}
func (s *Service) Get(user, workshop int64) (Job, error) {
	if e := auth.Require(s.DB, user, workshop, auth.MarketplaceRead); e != nil {
		return Job{}, e
	}
	j, e := scanJob(s.DB.QueryRow(`SELECT `+jobColumns+` FROM background_jobs WHERE workshop_id=? AND job_type=?`, workshop, JobType))
	if errors.Is(e, sql.ErrNoRows) {
		return Job{}, nil
	}
	return j, e
}
func (s *Service) Create(user, workshop int64) (Job, error) {
	return s.CreateJob(user, workshop, CreateInput{Type: JobType, NotificationEnabled: true})
}
func (s *Service) UpdateJob(user, workshop, id int64, action, local, zone string, threshold *int) error {
	tx, e := s.DB.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	j, e := scanJob(tx.QueryRow(`SELECT `+jobColumns+` FROM background_jobs WHERE id=? AND workshop_id=?`, id, workshop))
	if e != nil {
		return ErrScope
	}
	ex := s.Registry.Lookup(j.Type)
	if ex == nil {
		return ErrInput
	}
	_, manage := ex.Permissions()
	if e = auth.Require(tx, user, workshop, manage); e != nil {
		return e
	}
	if e = active(tx, user, workshop); e != nil {
		return e
	}
	switch action {
	case "pause":
		if j.Status != "active" {
			return ErrInput
		}
		j.Status = "paused"
	case "resume":
		if j.Status != "paused" {
			return ErrInput
		}
		j.Status = "active"
	case "cancel":
		j.Status = "cancelled"
	case "time":
		if j.Status == "cancelled" {
			return ErrInput
		}
		j.LocalTime = local
		j.Timezone = zone
	default:
		return ErrInput
	}
	next, e := Next(s.Now(), j.LocalTime, j.Timezone, true)
	if e != nil {
		return e
	}
	if j.Status == "active" {
		j.NextRun = next.Unix()
	}
	if action == "time" {
		if _, e = tx.Exec(`UPDATE workshop_settings SET timezone=? WHERE workshop_id=?`, zone, workshop); e != nil {
			return e
		}
		if threshold != nil {
			if j.Type != JobType || *threshold < 0 || *threshold > 1000000 {
				return ErrInput
			}
			if _, e = tx.Exec(`UPDATE workshop_settings SET wb_low_stock_threshold=? WHERE workshop_id=?`, *threshold, workshop); e != nil {
				return e
			}
		}
	}
	_, e = tx.Exec(`UPDATE background_jobs SET status=?,local_time=?,timezone=?,next_run_at=?,updated_at=? WHERE id=? AND workshop_id=?`, j.Status, j.LocalTime, j.Timezone, j.NextRun, s.Now().Unix(), id, workshop)
	if e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Service) PauseJob(u, w, id int64) error { return s.UpdateJob(u, w, id, "pause", "", "", nil) }
func (s *Service) ResumeJob(u, w, id int64) error {
	return s.UpdateJob(u, w, id, "resume", "", "", nil)
}
func (s *Service) CancelJob(u, w, id int64) error {
	return s.UpdateJob(u, w, id, "cancel", "", "", nil)
}
func (s *Service) Change(user, workshop int64, action, local, zone string, threshold int) error {
	j, e := s.Get(user, workshop)
	if e != nil {
		return e
	}
	return s.UpdateJob(user, workshop, j.ID, action, local, zone, &threshold)
}
func (s *Service) RunJobNow(ctx context.Context, user, workshop, id int64) error {
	j, e := s.GetJob(user, workshop, id)
	if e != nil {
		return e
	}
	_, manage := s.Registry.Lookup(j.Type).Permissions()
	if e = auth.Require(s.DB, user, workshop, manage); e != nil {
		return e
	}
	if e = active(s.DB, user, workshop); e != nil {
		return e
	}
	return s.Scheduler.execute(ctx, id, true)
}
func (s *Service) RunNow(ctx context.Context, user, workshop int64) error {
	j, e := s.Get(user, workshop)
	if e != nil || j.ID == 0 {
		return ErrScope
	}
	return s.RunJobNow(ctx, user, workshop, j.ID)
}

// Compatibility entrypoints delegate to the one engine owned by the application.
func (s *Service) Start(ctx context.Context)      { s.Scheduler.Start(ctx) }
func (s *Service) Close()                         { s.Scheduler.Close() }
func (s *Service) Tick(ctx context.Context) error { return s.Scheduler.Tick(ctx) }
