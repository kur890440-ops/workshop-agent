package background

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
	"workshop-agent/internal/auth"
	"workshop-agent/internal/integrations/mcpclient"
	wb "workshop-agent/internal/marketplace/wildberries"
)

const JobType = "WB_DAILY_SYNC"

type MCP interface {
	Prices(context.Context, string) (mcpclient.Envelope[[]wb.Price], error)
	Stocks(context.Context, string) (mcpclient.StocksResult, error)
}
type Aggregate struct {
	SellerItems   []SellerStockSummaryItem `json:"seller_items,omitempty"`
	PriceStatus   string                   `json:"price_status"`
	SellerSource  wb.StockInfo             `json:"seller_stock"`
	WBSource      wb.StockInfo             `json:"wb_stock"`
	SellerZero    int                      `json:"seller_zero_stock_count"`
	SellerLow     int                      `json:"seller_low_stock_count"`
	SellerChanges int                      `json:"seller_stock_changes_count"`
	ProductsCount int                      `json:"products_count"`
	PriceChanges  int                      `json:"price_changes_count"`
	StockChanges  int                      `json:"stock_changes_count"`
	ZeroStock     int                      `json:"zero_stock_count"`
	LowStock      int                      `json:"low_stock_count"`
	Errors        int                      `json:"errors_count"`
	PricesOK      bool                     `json:"prices_ok"`
	StocksOK      bool                     `json:"stocks_ok"`
}
type WBParameters struct {
	ConnectionID int64 `json:"connection_id"`
}
type wbJob struct {
	Job
	ConnectionID int64
}
type WBDailySyncExecutor struct {
	DB    *sql.DB
	MCP   MCP
	Tools PipelineTools
	Now   func() time.Time
}

func (*WBDailySyncExecutor) Permissions() (auth.Permission, auth.Permission) {
	return auth.MarketplaceRead, auth.MarketplaceManage
}
func (*WBDailySyncExecutor) Validate(q auth.Querier, user, workshop int64, raw string) (string, error) {
	var p WBParameters
	if raw != "{}" && strictJSON(raw, &p) != nil {
		return "", ErrInput
	}
	var id int64
	var seller string
	if q.QueryRow(`SELECT id,seller_id FROM marketplace_connections WHERE workshop_id=? AND enabled=1 AND provider='wildberries'`, workshop).Scan(&id, &seller) != nil || seller == "" || p.ConnectionID != 0 && p.ConnectionID != id {
		return "", ErrScope
	}
	p.ConnectionID = id
	data, _ := json.Marshal(p)
	return string(data), nil
}
func (*WBDailySyncExecutor) Check(q auth.Querier, j Job) error {
	var p WBParameters
	if strictJSON(j.Parameters, &p) != nil {
		return ErrInput
	}
	_, e := check(q, wbJob{j, p.ConnectionID}, 0)
	return e
}
func (s *WBDailySyncExecutor) Execute(ctx context.Context, job Job, run int64, owner string) ExecutionResult {
	return s.pipelineExecution(ctx, job, run, owner)
}

func check(q auth.Querier, j wbJob, revision int64) (string, error) {
	if e := auth.Require(q, j.CreatedBy, j.WorkshopID, auth.MarketplaceRead); e != nil {
		return "", e
	}
	var seller string
	var rev int64
	e := q.QueryRow(`SELECT seller_id,revision FROM marketplace_connections WHERE id=? AND workshop_id=? AND enabled=1 AND provider='wildberries'`, j.ConnectionID, j.WorkshopID).Scan(&seller, &rev)
	if e != nil || seller == "" || revision != 0 && rev != revision {
		return "", ErrScope
	}
	var status string
	if q.QueryRow(`SELECT status FROM background_jobs WHERE id=? AND workshop_id=?`, j.ID, j.WorkshopID).Scan(&status) != nil || status == "cancelled" {
		return "", ErrScope
	}
	return seller, nil
}
func safe(err error) string {
	var partial *wb.StockPartialError
	if errors.As(err, &partial) {
		switch partial.Batch.Info.Error {
		case "invalid_response":
			return "invalid_response"
		case "forbidden":
			return "wb_access_denied"
		case "rate_limited":
			return "WB_RATE_LIMITED"
		}
	}

	if err == nil {
		return ""
	}
	var rate *wb.RateLimitError
	if errors.As(err, &rate) {
		return "WB_RATE_LIMITED"
	}
	var e mcpclient.Error
	if errors.As(err, &e) {
		switch e {
		case "wb_identity_mismatch", "wb_configuration_error", "wb_authentication_error", "wb_access_denied", "wb_rate_limit", "wb_timeout", "wb_api_error", "mcp_connection_error", "mcp_busy", "pipeline_local_step_failed", "mcp_invalid_result":
			return string(e)
		}
	}
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	return "sync_failed"
}
