package telegram

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
	"workshop-agent/internal/llm"
	q "workshop-agent/internal/marketplacequery"
)

type queryStub struct {
	calls []q.Tool
	name  string
}

func (s *queryStub) QueryTools() []q.Tool {
	out := q.Registry()
	for i := range out {
		out[i].Available = true
	}
	return out
}
func (s *queryStub) QueryCall(_ context.Context, _ q.Scope, t q.Tool) (q.SourceResult, error) {
	s.calls = append(s.calls, t)
	if s.name != "" {
		return q.SourceResult{Status: "SUCCESS", Rows: []q.Row{{ProductID: 77, SKU: 77, Name: s.name, Quantity: 8, Confirmed: true, CapturedAt: time.Now().UTC().Format(time.RFC3339)}, {ProductID: 78, SKU: 78, Name: "Мыльница", Quantity: 99, Confirmed: true, CapturedAt: time.Now().UTC().Format(time.RFC3339)}}}, nil
	}
	return q.SourceResult{Status: "SUCCESS", Rows: []q.Row{{ProductID: 77, Name: "Проверочный товар", Quantity: 2, Confirmed: true, CapturedAt: time.Now().UTC().Format(time.RFC3339)}}}, nil
}
func TestDay20SemanticTelegramAndTrace(t *testing.T) {
	h, model, w := semanticFixture(t)
	stub := &queryStub{}
	h.bot.MarketQuery = &q.MarketplaceOrchestrator{Store: &q.Repository{DB: h.bot.WS.DB()}, MCP: stub}
	five := int64(5)
	in := q.MarketplaceQueryIntent{Marketplaces: []q.Provider{q.WB, q.Ozon}, QueryType: q.Stocks, StockSource: q.Seller, Filters: q.Filters{Operator: "LT", Quantity: &five}, Grouping: "MARKETPLACE", Sorting: "NAME", ComparisonMode: "NONE", IncludeZeroStock: true}
	raw, _ := json.Marshal(llm.StructuredCommand{Action: "marketplace_query", Marketplace: &in})
	model.raw = string(raw)
	before := model.calls
	h.message(t, 900001, "Покажи товары с остатком меньше 5 на Wildberries и Озон")
	h.bot.wbWait.Wait()
	if model.calls != before+1 || len(stub.calls) != 2 || stub.calls[0].Provider != q.WB || stub.calls[1].Provider != q.Ozon {
		t.Fatal("semantic query bypassed", stub.calls)
	}
	answer := lastAnswer(h)
	if !strings.Contains(answer, "Проверочный товар") || strings.Contains(answer, "wb_get_") {
		t.Fatal(answer)
	}
	trace, e := h.bot.MarketQuery.Store.LastTrace(q.Scope{UserID: 1, WorkshopID: w})
	if e != nil || len(trace.Steps) != 2 {
		t.Fatal(trace, e)
	}
	h.message(t, 900001, "/mcp_trace")
	if !strings.Contains(lastAnswer(h), "trace_id") {
		t.Fatal("missing admin trace")
	}
	stub.name = "Трап душевой"
	in.ProductName = "трап"
	in.Total = true
	in.Filters = q.Filters{Operator: "NONE"}
	raw, _ = json.Marshal(llm.StructuredCommand{Action: "marketplace_query", Marketplace: &in})
	model.raw = string(raw)
	h.message(t, 900001, "сколько всего трапов?")
	h.bot.wbWait.Wait()
	if !strings.Contains(model.context, "Marketplace query context") {
		t.Fatal("follow-up lost marketplace context")
	}
	answer = lastAnswer(h)
	if strings.Count(answer, "Всего по этому источнику: 8 шт.") != 2 || strings.Contains(answer, "Мыльница") {
		t.Fatal(answer)
	}
}
