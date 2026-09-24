package background

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
	"workshop-agent/internal/integrations/mcpclient"
)

// Run records execution metadata; source snapshots remain in separate tables.
type Run struct {
	ID, JobID, StartedAt, FinishedAt, DurationMS               int64
	Status, ResultJSON, AggregateJSON, ErrorCode, ErrorMessage string
}

func (r *Repository) LastRun(workshop, job int64) (out Run, e error) {
	e = r.DB.QueryRow(`SELECT id,job_id,started_at,finished_at,duration_ms,status,result_json,aggregate_json,error_code,error_message FROM background_job_runs WHERE workshop_id=? AND job_id=? ORDER BY id DESC LIMIT 1`, workshop, job).Scan(&out.ID, &out.JobID, &out.StartedAt, &out.FinishedAt, &out.DurationMS, &out.Status, &out.ResultJSON, &out.AggregateJSON, &out.ErrorCode, &out.ErrorMessage)
	if errors.Is(e, sql.ErrNoRows) {
		e = nil
	}
	return
}

// WBTrace is an explicit debug view, not extra implementation detail in normal UI.
func (s *Service) WBTrace(user, workshop int64) (string, error) {
	j, e := s.Get(user, workshop)
	if e != nil {
		return "", e
	}
	if j.ID == 0 {
		return "Задание не создано.", nil
	}
	run, e := s.Repository.LastRun(workshop, j.ID)
	if e != nil {
		return "", e
	}
	var count int
	if e = s.DB.QueryRow(`SELECT COUNT(*) FROM wb_daily_snapshots WHERE workshop_id=? AND run_id=?`, workshop, run.ID).Scan(&count); e != nil {
		return "", e
	}
	loc, e := time.LoadLocation(j.Timezone)
	if e != nil {
		return "", ErrInput
	}
	return fmt.Sprintf("JOB #%d\nTYPE %s\nSTATUS %s\nSCHEDULE daily %s\nTIMEZONE %s\nLAST RUN #%d %s\nPRICE TOOL %s\nSTOCK TOOL %s\nSNAPSHOTS %d\nNEXT RUN %s", j.ID, j.Type, j.Status, j.LocalTime, j.Timezone, run.ID, run.Status, mcpclient.PricesTool, mcpclient.StocksTool, count, time.Unix(j.NextRun, 0).In(loc).Format(time.RFC3339)), nil
}
