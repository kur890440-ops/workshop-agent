package background

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"sync"
	"time"
)

// Scheduler is the single engine. Executors own business logic, not clocks.
type Scheduler struct {
	*Service
	mu     sync.Mutex
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func (s *Scheduler) Start(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		return
	}
	ctx, s.cancel = context.WithCancel(ctx)
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			_ = s.Tick(ctx)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}
func (s *Scheduler) Close() {
	s.mu.Lock()
	if s.cancel != nil {
		s.cancel()
	}
	s.mu.Unlock()
	s.wg.Wait()
}
func (s *Scheduler) Tick(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	now := s.Now().Unix()
	// Expired workers lose write authority. History remains one run per date.
	_, e := s.DB.Exec(`UPDATE background_job_runs SET status='failed',finished_at=?,error_code='interrupted',error_message='Работа прервана; повтор за эту дату не создаётся' WHERE status='running' AND lease_until<=?`, now, now)
	if e != nil {
		return e
	}
	rows, e := s.DB.Query(`SELECT id FROM background_jobs WHERE status='active' AND next_run_at<=? ORDER BY next_run_at`, now)
	if e != nil {
		return e
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return e
		}
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, id := range ids {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		_ = s.execute(ctx, id, false)
	}
	return nil
}
func (s *Scheduler) execute(ctx context.Context, id int64, manual bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	tx, e := s.DB.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	j, e := scanJob(tx.QueryRow(`SELECT `+jobColumns+` FROM background_jobs WHERE id=?`, id))
	if e != nil {
		return ErrScope
	}
	if s.Registry.Lookup(j.Type) == nil || j.Schedule != "DAILY_AT_TIME" || j.Status != "active" && !(manual && j.Status == "paused") {
		return ErrScope
	}
	now := s.Now()
	if !manual && j.NextRun > now.Unix() {
		return nil
	}
	loc, e := time.LoadLocation(j.Timezone)
	if e != nil {
		return ErrInput
	}
	date := now.In(loc).Format("2006-01-02")
	next, e := Next(now, j.LocalTime, j.Timezone, false)
	if e != nil {
		return e
	}
	if !manual {
		today, _ := Next(now, j.LocalTime, j.Timezone, true)
		if today.After(now) {
			if _, e = tx.Exec(`UPDATE background_jobs SET next_run_at=? WHERE id=?`, today.Unix(), j.ID); e != nil {
				return e
			}
			return tx.Commit()
		}
	}
	var random [16]byte
	if _, e = rand.Read(random[:]); e != nil {
		return e
	}
	owner := hex.EncodeToString(random[:])
	res, e := tx.Exec(`INSERT INTO background_job_runs(job_id,workshop_id,job_type,local_date,status,started_at,lease_owner,lease_until) VALUES(?,?,?,?,'running',?,?,?) ON CONFLICT DO NOTHING`, j.ID, j.WorkshopID, j.Type, date, now.Unix(), owner, now.Add(2*time.Minute).Unix())
	if e != nil {
		return e
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		_, e = tx.Exec(`UPDATE background_jobs SET next_run_at=? WHERE id=?`, next.Unix(), j.ID)
		if e != nil {
			return e
		}
		if e = tx.Commit(); e != nil {
			return e
		}
		return ErrBusy
	}
	run, _ := res.LastInsertId()
	j.NextRun = next.Unix()
	if _, e = tx.Exec(`UPDATE background_jobs SET last_run_at=?,next_run_at=?,updated_at=? WHERE id=?`, now.Unix(), j.NextRun, now.Unix(), j.ID); e != nil {
		return e
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()
	heartDone := make(chan struct{})
	go func() {
		defer close(heartDone)
		ticker := time.NewTicker(20 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := s.Registry.Lookup(j.Type).Check(s.DB, j); err != nil {
					cancel()
					return
				}
				r, err := s.DB.Exec(`UPDATE background_job_runs SET lease_until=? WHERE id=? AND lease_owner=? AND status='running' AND lease_until>?`, s.Now().Add(2*time.Minute).Unix(), run, owner, s.Now().Unix())
				if err != nil {
					cancel()
					return
				}
				n, _ := r.RowsAffected()
				if n != 1 {
					cancel()
					return
				}
			}
		}
	}()
	defer func() { cancel(); <-heartDone }()
	result := ExecutionResult{Status: "failed", ErrorCode: "access_or_configuration", ResultJSON: "{}", AggregateJSON: "{}"}
	if e := s.Registry.Lookup(j.Type).Check(s.DB, j); e == nil {
		result = s.Registry.Lookup(j.Type).Execute(ctx, j, run, owner)
	}
	if (result.Status != "success" && result.Status != "partial_success" && result.Status != "failed") || !json.Valid([]byte(result.ResultJSON)) || !json.Valid([]byte(result.AggregateJSON)) || len(result.ResultJSON) > 1<<20 || len(result.AggregateJSON) > 65536 {
		result = ExecutionResult{Status: "failed", ErrorCode: "invalid_executor_result", ResultJSON: "{}", AggregateJSON: "{}"}
	}
	status := result.Status
	raw := result.ResultJSON
	araw := result.AggregateJSON
	code := result.ErrorCode
	finished := s.Now()
	final, e := s.DB.Begin()
	if e != nil {
		return e
	}
	defer final.Rollback()
	res, e = final.Exec(`UPDATE background_job_runs SET status=?,finished_at=?,duration_ms=?,result_json=?,aggregate_json=?,error_code=?,error_message=? WHERE id=? AND lease_owner=? AND status='running' AND lease_until>?`, status, finished.Unix(), finished.Sub(now).Milliseconds(), string(raw), string(araw), code, code, run, owner, finished.Unix())
	if e != nil {
		return e
	}
	n, _ = res.RowsAffected()
	if n != 1 {
		return ErrBusy
	}
	_, e = final.Exec(`UPDATE background_jobs SET last_result=?,last_success_at=CASE WHEN ?='success' THEN ? ELSE last_success_at END,updated_at=? WHERE id=?`, status, status, finished.Unix(), finished.Unix(), j.ID)
	if e != nil {
		return e
	}
	if e = final.Commit(); e != nil {
		return e
	}
	if s.Sender == nil || !j.NotificationEnabled {
		_, _ = s.DB.Exec(`UPDATE background_job_runs SET notification_state='suppressed' WHERE id=?`, run)
	}
	if s.Sender != nil && j.NotificationEnabled {
		if denied := s.Registry.Lookup(j.Type).Check(s.DB, j); denied == nil && (result.CanNotify == nil || result.CanNotify()) {
			state := "sent"
			if s.Sender.Send(ctx, j.CreatedBy, j.WorkshopID, result.Summary) != nil {
				state = "failed"
			}
			_, _ = s.DB.Exec(`UPDATE background_job_runs SET notification_state=? WHERE id=?`, state, run)
		} else {
			_, _ = s.DB.Exec(`UPDATE background_job_runs SET notification_state='suppressed' WHERE id=?`, run)
		}
	}
	return nil
}
