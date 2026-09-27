package background

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"workshop-agent/internal/auth"
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
	if e := auth.Require(s.DB, user, workshop, auth.MarketplaceManage); e != nil {
		return "", e
	}
	var pipeline PipelineResult
	if json.Unmarshal([]byte(run.ResultJSON), &pipeline) == nil && pipeline.Name == MarketPipeline {
		return PipelineTrace(pipeline), nil
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

func PipelineTrace(p PipelineResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "PIPELINE %s\nSTATUS %s\nSTART %s\nFINISH %s\nTOTAL %d ms\n", p.Name, p.Status, p.StartedAt, p.FinishedAt, p.DurationMS)
	for _, s := range p.Steps {
		fmt.Fprintf(&b, "\n%s: %s (%d ms)\n", s.Tool, s.Status, s.DurationMS)
		if s.Status == "SKIPPED" {
			continue
		}
		fmt.Fprintf(&b, "%s → %s\n", s.StartedAt, s.FinishedAt)
		if s.Tool == BuildTool || s.Tool == SaveTool {
			fmt.Fprintf(&b, "INPUT prices=%d seller=%d wb=%d\n", s.Input.Prices, s.Input.SellerStocks, s.Input.Stocks)
		}
		if s.Tool == mcpclient.PricesTool {
			fmt.Fprintf(&b, "OUTPUT prices=%d\n", s.Output.Prices)
		}
		if s.Tool == mcpclient.StocksTool {
			fmt.Fprintf(&b, "OUTPUT stocks=%d\n", s.Output.Stocks)
		}

		if s.Output.StockInfo != nil {
			v := s.Output.StockInfo
			fmt.Fprintf(&b, "SOURCE %s %s received=%d valid=%d invalid=%d missing=%d HTTP=%d\n", v.Source, v.Status, v.Received, v.Valid, v.Invalid, v.Missing, v.HTTPStatus)
		}
		if s.Output.Aggregate != nil {
			a := s.Output.Aggregate
			fmt.Fprintf(&b, "products=%d price_changes=%d stock_changes=%d zero=%d low=%d\n", a.ProductsCount, a.PriceChanges, a.StockChanges, a.ZeroStock, a.LowStock)
		}
		if s.Output.Saved != nil {
			fmt.Fprintf(&b, "saved=true prices=%d seller=%d wb=%d\n", s.Output.Saved.PriceRecords, s.Output.Saved.SellerRecords, s.Output.Saved.StockRecords)
		}
	}
	if p.Error != nil {
		fmt.Fprintf(&b, "ERROR %s retry_not_before=%s\n", p.Error.Code, p.Error.RetryNotBefore)
	}
	return b.String()
}
