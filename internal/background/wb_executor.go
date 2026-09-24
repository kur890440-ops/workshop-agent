package background

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
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
	ProductsCount int  `json:"products_count"`
	PriceChanges  int  `json:"price_changes_count"`
	StockChanges  int  `json:"stock_changes_count"`
	ZeroStock     int  `json:"zero_stock_count"`
	LowStock      int  `json:"low_stock_count"`
	Errors        int  `json:"errors_count"`
	PricesOK      bool `json:"prices_ok"`
	StocksOK      bool `json:"stocks_ok"`
}
type WBParameters struct {
	ConnectionID int64 `json:"connection_id"`
}
type wbJob struct {
	Job
	ConnectionID int64
}
type WBDailySyncExecutor struct {
	DB  *sql.DB
	MCP MCP
	Now func() time.Time
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
	if q.QueryRow(`SELECT id,seller_id FROM marketplace_connections WHERE workshop_id=? AND enabled=1`, workshop).Scan(&id, &seller) != nil || seller == "" || p.ConnectionID != 0 && p.ConnectionID != id {
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
	var p WBParameters
	if strictJSON(job.Parameters, &p) != nil {
		return ExecutionResult{Status: "failed", ErrorCode: "invalid_parameters", ResultJSON: "{}", AggregateJSON: "{}"}
	}
	j := wbJob{job, p.ConnectionID}
	var revision int64
	_ = s.DB.QueryRow(`SELECT revision FROM marketplace_connections WHERE id=? AND workshop_id=?`, j.ConnectionID, j.WorkshopID).Scan(&revision)
	agg := Aggregate{}
	products := map[int64]bool{}
	results := map[string]string{}
	seller, authErr := check(s.DB, j, revision)
	if authErr != nil || s.MCP == nil {
		agg.Errors = 2
		results[mcpclient.PricesTool] = "access_or_configuration"
		results[mcpclient.StocksTool] = "access_or_configuration"
	} else {
		prices, err := s.MCP.Prices(ctx, seller)
		if err == nil {
			err = s.savePrices(j, revision, run, owner, prices, &agg, products)
		}
		results[mcpclient.PricesTool] = safe(err)
		if err != nil {
			agg.Errors++
		} else {
			agg.PricesOK = true
		}
		if _, err = check(s.DB, j, revision); err == nil {
			var stocks mcpclient.StocksResult
			stocks, err = s.MCP.Stocks(ctx, seller)
			if err == nil {
				err = s.saveStocks(j, revision, run, owner, stocks.Value, &agg, products)
			}
		}
		results[mcpclient.StocksTool] = safe(err)
		if err != nil {
			agg.Errors++
		} else {
			agg.StocksOK = true
		}
	}
	agg.ProductsCount = len(products)
	status := "success"
	if agg.Errors == 2 {
		status = "failed"
	} else if agg.Errors > 0 {
		status = "partial_success"
	}
	raw, _ := json.Marshal(results)
	araw, _ := json.Marshal(agg)

	code := ""
	if agg.Errors > 0 {
		code = "source_failed"
	}
	return ExecutionResult{Status: status, ResultJSON: string(raw), AggregateJSON: string(araw), ErrorCode: code, Summary: Summary(job, agg, status), CanNotify: func() bool { _, e := check(s.DB, j, revision); return e == nil }}
}
func check(q auth.Querier, j wbJob, revision int64) (string, error) {
	if e := auth.Require(q, j.CreatedBy, j.WorkshopID, auth.MarketplaceRead); e != nil {
		return "", e
	}
	var seller string
	var rev int64
	e := q.QueryRow(`SELECT seller_id,revision FROM marketplace_connections WHERE id=? AND workshop_id=? AND enabled=1`, j.ConnectionID, j.WorkshopID).Scan(&seller, &rev)
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
	if err == nil {
		return ""
	}
	var e mcpclient.Error
	if errors.As(err, &e) {
		switch e {
		case "wb_configuration_error", "wb_authentication_error", "wb_access_denied", "wb_rate_limit", "wb_timeout", "wb_api_error", "mcp_connection_error", "mcp_busy":
			return string(e)
		}
	}
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	return "sync_failed"
}
func Summary(j Job, a Aggregate, status string) string {
	stocks := "Остатки: загрузка не выполнена; прежние данные сохранены."
	if a.StocksOK {
		stocks = fmt.Sprintf("Остатки: изменилось — %d; нулевых — %d; низких — %d.", a.StockChanges, a.ZeroStock, a.LowStock)
	}
	prices := "Цены: загрузка не выполнена; прежние данные сохранены."
	if a.PricesOK {
		prices = fmt.Sprintf("Цены: изменилось — %d.", a.PriceChanges)
	}
	loc, e := time.LoadLocation(j.Timezone)
	if e != nil {
		loc = time.UTC
	}
	return fmt.Sprintf("Wildberries · Утренняя синхронизация\nРезультат: %s\nОбновлено товаров: %d\n%s\n%s\nОшибок: %d\nСледующий запуск: %s (%s)\nИсточник: WB через MCP. Остатки цеха не изменены.", status, a.ProductsCount, prices, stocks, a.Errors, time.Unix(j.NextRun, 0).In(loc).Format("02.01.2006 15:04"), j.Timezone)
}
