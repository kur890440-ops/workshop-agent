package background

import (
	"context"
	"testing"
	"time"
	"workshop-agent/internal/auth"
)

type otherExecutor struct{ calls int }

func (*otherExecutor) Permissions() (auth.Permission, auth.Permission) {
	return auth.WorkshopRead, auth.WorkshopManage
}
func (*otherExecutor) Validate(_ auth.Querier, _, _ int64, raw string) (string, error) {
	if raw != "{}" {
		return "", ErrInput
	}
	return raw, nil
}
func (*otherExecutor) Check(q auth.Querier, j Job) error {
	return auth.Require(q, j.CreatedBy, j.WorkshopID, auth.WorkshopRead)
}
func (e *otherExecutor) Execute(context.Context, Job, int64, string) ExecutionResult {
	e.calls++
	return ExecutionResult{Status: "success", ResultJSON: `{"other":true}`, AggregateJSON: `{}`, Summary: "Other job"}
}

func TestOneSchedulerHandlesTwoTypesAndGenericManagement(t *testing.T) {
	s, _, m, sender, u, w := fixture(t)
	other := &otherExecutor{}
	if e := s.Registry.Register("OTHER_JOB", other); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Create(u, w); e != nil {
		t.Fatal(e)
	}
	j, e := s.CreateJob(u, w, CreateInput{Type: "OTHER_JOB", LocalTime: "08:00", NotificationEnabled: false})
	if e != nil {
		t.Fatal(e)
	}
	if e = s.PauseJob(u, w, j.ID); e != nil {
		t.Fatal(e)
	}
	if e = s.Tick(context.Background()); e != nil {
		t.Fatal(e)
	}
	if other.calls != 0 || len(m.calls) != 2 {
		t.Fatal("pause or dispatch", other.calls, m.calls)
	}
	if e = s.ResumeJob(u, w, j.ID); e != nil {
		t.Fatal(e)
	}
	if e = s.Tick(context.Background()); e != nil {
		t.Fatal(e)
	}
	if e = s.Tick(context.Background()); e != nil {
		t.Fatal(e)
	}
	if other.calls != 1 || len(sender.messages) != 1 || scalar(t, s, `SELECT COUNT(*) FROM background_job_runs`) != 2 {
		t.Fatal("duplicate or notifications", other.calls, sender.messages)
	}
	if jobs, e := s.ListJobs(u, w); e != nil || len(jobs) != 2 {
		t.Fatal(jobs, e)
	}
	if _, e = s.GetJob(u+999, w, j.ID); e == nil {
		t.Fatal("scope bypass")
	}
	if e = s.CancelJob(u, w, j.ID); e != nil {
		t.Fatal(e)
	}
	s.Now = func() time.Time { return time.Date(2026, 9, 25, 6, 0, 0, 0, time.UTC) }
	if e = s.Tick(context.Background()); e != nil {
		t.Fatal(e)
	}
	if other.calls != 1 {
		t.Fatal("cancel failed")
	}
	if scalar(t, s, `SELECT COUNT(*) FROM background_job_runs WHERE job_type='OTHER_JOB'`) != 1 {
		t.Fatal("job type lost")
	}
}
