package background

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"
	"workshop-agent/internal/integrations/mcpclient"
	wb "workshop-agent/internal/marketplace/wildberries"
)

const MarketPipeline = "WB_MARKET_SYNC_PIPELINE"
const BuildTool = "wb_build_market_summary"
const SaveTool = "wb_save_market_snapshot"

type MarketInput struct {
	Seller      wb.StockBatch                  `json:"seller"`
	WBInfo      wb.StockInfo                   `json:"wb_info"`
	PriceStatus string                         `json:"price_status"`
	Prices      mcpclient.Envelope[[]wb.Price] `json:"prices"`
	Stocks      mcpclient.Envelope[[]wb.Stock] `json:"stocks"`
}
type SaveInput struct {
	Market    MarketInput `json:"market"`
	Aggregate Aggregate   `json:"aggregate"`
}
type SaveResult struct {
	SellerRecords  int  `json:"seller_records_saved"`
	Saved          bool `json:"saved"`
	PriceRecords   int  `json:"price_records_saved"`
	StockRecords   int  `json:"stock_records_saved"`
	AggregateSaved bool `json:"aggregate_saved"`
	CurrentUpdated int  `json:"current_state_updated"`
}

// PipelineContext is trusted application state, never MCP arguments or LLM input.
type PipelineContext struct {
	Job                           Job
	ConnectionID, Revision, RunID int64
	Owner                         string
}
type PipelineTools interface {
	CallPipelineTool(context.Context, PipelineContext, string, any, any) error
}
type PipelineError struct {
	Code           string `json:"code"`
	RetryNotBefore string `json:"retry_not_before,omitempty"`
}
type StepSummary struct {
	StockInfo    *wb.StockInfo `json:"stock_info,omitempty"`
	SellerStocks int           `json:"seller_stocks"`
	Prices       int           `json:"prices"`
	Stocks       int           `json:"stocks"`
	Aggregate    *Aggregate    `json:"aggregate,omitempty"`
	Saved        *SaveResult   `json:"saved,omitempty"`
}
type PipelineStep struct {
	Key            string         `json:"step_key"`
	Tool           string         `json:"tool_name"`
	Required       bool           `json:"required"`
	Classification string         `json:"classification"`
	WBWrite        bool           `json:"wb_write"`
	Status         string         `json:"status"`
	StartedAt      string         `json:"started_at"`
	FinishedAt     string         `json:"finished_at"`
	DurationMS     int64          `json:"duration_ms"`
	Input          StepSummary    `json:"input"`
	Output         StepSummary    `json:"output"`
	Error          *PipelineError `json:"error,omitempty"`
}
type PipelineResult struct {
	Name       string         `json:"pipeline_name"`
	Status     string         `json:"status"`
	StartedAt  string         `json:"started_at"`
	FinishedAt string         `json:"finished_at"`
	DurationMS int64          `json:"duration_ms"`
	Steps      []PipelineStep `json:"steps"`
	Aggregate  *Aggregate     `json:"aggregate,omitempty"`
	SaveResult *SaveResult    `json:"save_result,omitempty"`
	Error      *PipelineError `json:"error,omitempty"`
}
type MCPPipelineRunner struct {
	Reads MCP
	Tools PipelineTools
	Check func() (string, error)
}

func (r MCPPipelineRunner) Run(ctx context.Context, scope PipelineContext) (out PipelineResult) {
	start := time.Now()
	out = PipelineResult{Name: MarketPipeline, Status: "FAILED", StartedAt: start.UTC().Format(time.RFC3339Nano)}
	defer func() {
		out.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
		out.DurationMS = time.Since(start).Milliseconds()
	}()
	names := []string{mcpclient.PricesTool, mcpclient.SellerStocksTool, mcpclient.StocksTool, BuildTool, SaveTool}
	for i, n := range names {
		class := "READ_ONLY"
		if i == 3 {
			class = "LOCAL_COMPUTE"
		}
		if i == 4 {
			class = "LOCAL_WRITE"
		}
		out.Steps = append(out.Steps, PipelineStep{Key: []string{"prices", "seller_stocks", "wb_stocks", "summary", "save"}[i], Tool: n, Required: i >= 3, Classification: class, Status: "SKIPPED"})
	}
	data := MarketInput{PriceStatus: "FAILED"}
	unavailable := false
	useful := false
	for i := range out.Steps {
		st := &out.Steps[i]
		began := time.Now()
		st.StartedAt = began.UTC().Format(time.RFC3339Nano)
		seller := ""
		err := ctx.Err()
		fatal := false
		if err == nil {
			if r.Check == nil || r.Reads == nil || r.Tools == nil {
				err = ErrScope
			} else {
				seller, err = r.Check()
			}
		}
		if err != nil {
			fatal = true
		}
		if err == nil {
			switch i {
			case 0:
				data.Prices, err = r.Reads.Prices(ctx, seller)
				if err == nil {
					err = validateMarketPart(data.Prices)
				}
				if err == nil {
					data.PriceStatus = "SUCCESS"
					useful = true
				} else {
					data.Prices = mcpclient.Envelope[[]wb.Price]{}
				}
				st.Output.Prices = len(data.Prices.Data)
			case 1:
				if reads, ok := r.Reads.(interface {
					StockSource(context.Context, string, wb.StockSource) (wb.StockBatch, error)
				}); ok {
					data.Seller, err = reads.StockSource(ctx, seller, wb.StockSeller)
				} else {
					err = ErrInput
					data.Seller = wb.NewStockBatch(wb.StockSeller, nil, err)
				}
				if data.Seller.Usable() {
					useful = true
				}
				st.Output.SellerStocks = len(data.Seller.Rows)
				st.Output.StockInfo = &data.Seller.Info
			case 2:
				v, e := r.Reads.Stocks(ctx, seller)
				err = e
				data.Stocks = v.Value
				if err == nil {
					err = validateMarketPart(data.Stocks)
				}
				batch := wb.NewStockBatch(wb.StockWB, data.Stocks.Data, err)
				data.WBInfo = batch.Info
				data.WBInfo.StartedAt = st.StartedAt
				data.WBInfo.DurationMS = time.Since(began).Milliseconds()
				data.WBInfo.Caller = wb.TraceMetadata(ctx).Caller
				st.Output.StockInfo = &data.WBInfo
				if err == nil {
					useful = true
					data.WBInfo.CapturedAt = data.Stocks.FetchedAt
				} else {
					data.Stocks = mcpclient.Envelope[[]wb.Stock]{}
				}
				st.Output.Stocks = len(data.Stocks.Data)
			case 3:
				st.Input = StepSummary{Prices: len(data.Prices.Data), Stocks: len(data.Stocks.Data), SellerStocks: len(data.Seller.Rows)}
				var a Aggregate
				err = r.Tools.CallPipelineTool(ctx, scope, BuildTool, data, &a)
				if err == nil {
					if a.ProductsCount < 0 || a.Errors < 0 {
						err = ErrInput
					} else {
						out.Aggregate = &a
						st.Output.Aggregate = &a
					}
				}
			case 4:
				st.Input = StepSummary{Prices: len(data.Prices.Data), Stocks: len(data.Stocks.Data), SellerStocks: len(data.Seller.Rows), Aggregate: out.Aggregate}
				var saved SaveResult
				err = r.Tools.CallPipelineTool(ctx, scope, SaveTool, SaveInput{data, *out.Aggregate}, &saved)
				if err == nil {
					if !saved.Saved || !saved.AggregateSaved || saved.PriceRecords != len(data.Prices.Data) || saved.StockRecords != len(data.Stocks.Data) || saved.SellerRecords != len(data.Seller.Rows) {
						err = ErrInput
					} else {
						out.SaveResult = &saved
						st.Output.Saved = &saved
					}
				}
			}
		}
		st.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
		st.DurationMS = time.Since(began).Milliseconds()
		if err != nil {
			if err.Error() == "wb_identity_mismatch" || errors.Is(err, wb.IdentityMismatch) {
				fatal = true
			}
			pe := &PipelineError{Code: safe(err)}
			var rate *wb.RateLimitError
			if errors.As(err, &rate) && !rate.RetryAt.IsZero() {
				pe.RetryNotBefore = rate.RetryAt.UTC().Format(time.RFC3339Nano)
			}
			st.Status = "FAILED"
			st.Error = pe
			if out.Error == nil || pe.RetryNotBefore > out.Error.RetryNotBefore {
				out.Error = pe
			}
			if i == 1 {
				st.Status = data.Seller.Info.Status
			}
			if i == 2 {
				st.Status = data.WBInfo.Status
			}
			unavailable = true
			if fatal || ctx.Err() != nil || i >= 3 {
				return
			}
		} else {
			st.Status = "SUCCESS"
			if i == 1 && data.Seller.Info.Status != "SUCCESS" {
				st.Status = data.Seller.Info.Status
				unavailable = true
			}
		}
	}
	if useful {
		out.Status = "SUCCESS"
		if unavailable {
			out.Status = "PARTIAL_SUCCESS"
		}
	}
	return
}
func validateMarketPart[T any](v mcpclient.Envelope[[]T]) error {
	if !v.Complete || !v.UntrustedData || v.Source != "wildberries" || v.Data == nil || len(v.Data) > 50000 {
		return ErrInput
	}
	_, err := time.Parse(time.RFC3339Nano, v.FetchedAt)
	if err != nil {
		return ErrInput
	}
	seen := map[string]bool{}
	switch data := any(v.Data).(type) {
	case []wb.Price:
		for _, p := range data {
			key := fmt.Sprintf("%d:%d", p.NmID, p.SizeID)
			if p.NmID <= 0 || p.SizeID <= 0 || p.PriceCents < 0 || len(p.Currency) != 3 || p.DiscountedCents != nil && *p.DiscountedCents < 0 || seen[key] {
				return ErrInput
			}
			seen[key] = true
		}
	case []wb.Stock:
		totals := map[int64]int64{}
		for _, p := range data {
			key := fmt.Sprintf("%d:%d:%d", p.NmID, p.ChrtID, p.WarehouseID)
			if p.NmID <= 0 || p.ChrtID <= 0 || p.WarehouseID <= 0 || p.Quantity < 0 || p.Quantity > math.MaxInt64-totals[p.NmID] || seen[key] {
				return ErrInput
			}
			seen[key] = true
			totals[p.NmID] += p.Quantity
		}
	}
	return nil
}
func (s *WBDailySyncExecutor) pipelineExecution(ctx context.Context, job Job, run int64, owner string) ExecutionResult {
	var p WBParameters
	if strictJSON(job.Parameters, &p) != nil {
		return ExecutionResult{Status: "failed", ErrorCode: "invalid_parameters", ResultJSON: "{}", AggregateJSON: "{}"}
	}
	scope := PipelineContext{Job: job, ConnectionID: p.ConnectionID, RunID: run, Owner: owner}
	_ = s.DB.QueryRow(`SELECT revision FROM marketplace_connections WHERE id=? AND workshop_id=?`, p.ConnectionID, job.WorkshopID).Scan(&scope.Revision)
	checker := func() (string, error) {
		if e := s.validatePipeline(s.DB, scope); e != nil {
			return "", e
		}
		return check(s.DB, wbJob{job, p.ConnectionID}, scope.Revision)
	}
	meta := wb.TraceMetadata(ctx)
	meta.WorkshopID = job.WorkshopID
	if meta.Caller != "run_now" {
		meta.Caller = "wb_daily_sync"
	}
	ctx = wb.WithTrace(ctx, meta)
	result := (MCPPipelineRunner{Reads: s.MCP, Tools: s.Tools, Check: checker}).Run(ctx, scope)
	a := Aggregate{Errors: 1}
	status := "failed"
	code, retry := "", ""
	if result.Status == "SUCCESS" || result.Status == "PARTIAL_SUCCESS" {
		a = *result.Aggregate
		status = "success"
		if result.Status == "PARTIAL_SUCCESS" {
			status = "partial_success"
		}
	}
	if result.Error != nil {
		code = result.Error.Code
		retry = result.Error.RetryNotBefore
	}
	raw, _ := json.Marshal(result)
	ar, _ := json.Marshal(a)
	summary := Summary(job, a, status)

	return ExecutionResult{Status: status, ResultJSON: string(raw), AggregateJSON: string(ar), ErrorCode: code, RetryNotBefore: retry, Summary: summary, CanNotify: func() bool { _, e := check(s.DB, wbJob{job, p.ConnectionID}, scope.Revision); return e == nil }}
}
