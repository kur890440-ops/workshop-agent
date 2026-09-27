package background

import (
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"time"
	"workshop-agent/internal/auth"
	wb "workshop-agent/internal/marketplace/wildberries"
)

type DailySummary struct {
	Code            string     `json:"code"`
	JobID           int64      `json:"job_id"`
	RunID           int64      `json:"run_id"`
	Status          string     `json:"status"`
	CapturedAt      string     `json:"captured_at"`
	NextRunAt       string     `json:"next_run_at"`
	JobStatus       string     `json:"job_status"`
	Timezone        string     `json:"timezone"`
	Aggregate       *Aggregate `json:"aggregate"`
	ErrorCategories []string   `json:"error_categories"`
	Summary         string     `json:"summary"`
}

// DailySummary reads a consistent, authorized snapshot of stored run metadata.
// No executor, HTTP, MCP price/stock call or recalculation is involved.
func (s *Service) DailySummary(user, workshop int64) (out DailySummary, err error) {
	out = DailySummary{Code: "NO_SUMMARY_AVAILABLE", ErrorCategories: []string{}, Summary: "Утренняя синхронизация еще не выполнялась."}
	tx, err := s.DB.Begin()
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if err = auth.Require(tx, user, workshop, auth.MarketplaceRead); err != nil {
		return out, err
	}
	if err = active(tx, user, workshop); err != nil {
		return out, err
	}
	j, err := scanJob(tx.QueryRow(`SELECT `+jobColumns+` FROM background_jobs WHERE workshop_id=? AND job_type=?`, workshop, JobType))
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	out.JobID = j.ID
	out.JobStatus = j.Status
	out.Timezone = j.Timezone
	if j.NextRun > 0 {
		out.NextRunAt = time.Unix(j.NextRun, 0).UTC().Format(time.RFC3339)
	}
	var captured int64
	var raw, results string
	err = tx.QueryRow(`SELECT id,status,finished_at,aggregate_json,result_json FROM background_job_runs WHERE job_id=? AND workshop_id=? AND job_type=? AND status IN ('success','partial_success') ORDER BY finished_at DESC,id DESC LIMIT 1`, j.ID, workshop, JobType).Scan(&out.RunID, &out.Status, &captured, &raw, &results)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	var aggregate Aggregate
	var fields map[string]json.RawMessage
	if len(raw) > 65536 || json.Unmarshal([]byte(raw), &fields) != nil {
		return out, ErrInput
	}
	for _, name := range []string{"products_count", "price_changes_count", "stock_changes_count", "zero_stock_count", "low_stock_count", "errors_count", "prices_ok", "stocks_ok"} {
		value, ok := fields[name]
		if !ok || string(value) == "null" {
			return out, ErrInput
		}
	}
	if len(raw) > 65536 || json.Unmarshal([]byte(raw), &aggregate) != nil || aggregate.ProductsCount < 0 || aggregate.PriceChanges < 0 || aggregate.StockChanges < 0 || aggregate.ZeroStock < 0 || aggregate.LowStock < 0 || aggregate.Errors < 0 || captured <= 0 {
		return out, ErrInput
	}
	if out.Status == "partial_success" && aggregate.Errors == 0 {
		return out, ErrInput
	}
	var categories map[string]string
	var pipeline PipelineResult
	if len(results) > 1<<20 {
		return out, ErrInput
	}
	if json.Unmarshal([]byte(results), &pipeline) == nil && pipeline.Name == MarketPipeline {
		if (pipeline.Status != "SUCCESS" && pipeline.Status != "PARTIAL_SUCCESS") || pipeline.Aggregate == nil || !reflect.DeepEqual(*pipeline.Aggregate, aggregate) || pipeline.SaveResult == nil || !pipeline.SaveResult.Saved {
			return out, ErrInput
		}
		categories = map[string]string{}
	} else if json.Unmarshal([]byte(results), &categories) != nil {
		return out, ErrInput
	}
	seen := map[string]bool{}
	for _, category := range categories {
		if category == "" {
			continue
		}
		// Never echo arbitrary persisted error text or payload fields.
		switch category {
		case "WB_RATE_LIMITED", "wb_identity_mismatch", "wb_configuration_error", "wb_authentication_error", "wb_access_denied", "wb_rate_limit", "wb_timeout", "wb_api_error", "mcp_connection_error", "mcp_busy", "cancelled", "sync_failed", "access_or_configuration":
		default:
			category = "source_failed"
		}
		if !seen[category] {
			out.ErrorCategories = append(out.ErrorCategories, category)
			seen[category] = true
		}
	}
	sort.Strings(out.ErrorCategories)
	// Old aggregates predate seller_items. Reconstruct from THIS run's immutable
	// SELLER snapshots only, never from today's current inventory.
	if _, exists := fields["seller_items"]; !exists && aggregate.SellerSource.Source == wb.StockSeller && aggregate.SellerSource.Valid > 0 {
		var params WBParameters
		if json.Unmarshal([]byte(j.Parameters), &params) != nil {
			return out, ErrInput
		}
		rows, e := tx.Query(`SELECT data_json FROM wb_daily_snapshots WHERE run_id=? AND workshop_id=? AND connection_id=? AND source_tool='wb_get_seller_stocks' ORDER BY item_key`, out.RunID, workshop, params.ConnectionID)
		if e != nil {
			return out, e
		}
		batch := wb.StockBatch{Info: aggregate.SellerSource}
		for rows.Next() {
			var data string
			var row wb.Stock
			if rows.Scan(&data) != nil || json.Unmarshal([]byte(data), &row) != nil {
				rows.Close()
				return out, ErrInput
			}
			batch.Rows = append(batch.Rows, row)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return out, e
		}
		if len(batch.Rows) > 0 {
			var threshold int64
			if e = tx.QueryRow(`SELECT wb_low_stock_threshold FROM workshop_settings WHERE workshop_id=?`, workshop).Scan(&threshold); e != nil {
				return out, e
			}
			aggregate.SellerItems, e = buildSellerItems(tx, workshop, params.ConnectionID, threshold, batch)
			if e != nil {
				return out, e
			}
		}
	}
	out.Code = "OK"
	out.Aggregate = &aggregate
	out.CapturedAt = time.Unix(captured, 0).UTC().Format(time.RFC3339)
	out.Summary = Summary(j, aggregate, out.Status)
	return out, nil
}
