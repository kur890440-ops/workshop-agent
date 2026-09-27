package background

import (
	"context"
	"testing"
	"time"
	wb "workshop-agent/internal/marketplace/wildberries"
)

func TestRateLimitedRunPersistsDeadlineAndSchedule(t *testing.T) {
	s, _, m, _, u, w := fixture(t)
	until := time.Now().Add(120 * time.Second).UTC().Truncate(time.Second)
	m.stockErr = &wb.RateLimitError{Operation: "analytics", RetryAt: until, Source: "wb_retry"}
	if _, e := s.Create(u, w); e != nil {
		t.Fatal(e)
	}
	if e := s.RunNow(context.Background(), u, w); e != nil {
		t.Fatal(e)
	}
	var status, code, retry string
	if e := s.DB.QueryRow(`SELECT status,error_code,retry_not_before FROM background_job_runs ORDER BY id DESC LIMIT 1`).Scan(&status, &code, &retry); e != nil {
		t.Fatal(e)
	}
	if status != "partial_success" || code != "WB_RATE_LIMITED" || retry != until.Format(time.RFC3339Nano) {
		t.Fatal(status, code, retry)
	}
	if scalar(t, s, `SELECT COUNT(*) FROM background_jobs WHERE status='active'`) != 1 {
		t.Fatal("schedule lost")
	}
	if len(m.calls) != 2 {
		t.Fatal("unbounded retry", m.calls)
	}
}
