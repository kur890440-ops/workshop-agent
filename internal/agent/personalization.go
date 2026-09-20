package agent

import (
	"context"
	"fmt"
	"strings"
	"time"
	"workshop-agent/internal/auth"
	"workshop-agent/internal/inventory"
	"workshop-agent/internal/llm"
	"workshop-agent/internal/memory"
	"workshop-agent/internal/personalization"
)

type CompletionClient interface {
	Complete(context.Context, string) (string, *llm.Usage, error)
}

func IsPersonalizedReport(text string) bool {
	t := strings.ToLower(text)
	return !strings.HasPrefix(t, "/") && (strings.Contains(t, "отчет") || strings.Contains(t, "отчёт") || strings.Contains(t, "сводк") || (strings.Contains(t, "материал") && strings.Contains(t, "оцени")))
}
func (a *WorkshopAgent) BuildReportContext(user, workshop, chat int64, text string) (memory.Context, error) {
	m := a.Memory.ForUser(user)
	sc, err := m.EnsureSession(memory.Scope{UserID: user, WorkshopID: workshop}, chat)
	if err != nil {
		return memory.Context{}, err
	}
	items, err := a.Inv.ForUser(user).ListMaterials(workshop)
	if err != nil {
		return memory.Context{}, err
	}
	facts := []map[string]any{}
	for _, p := range items {
		facts = append(facts, map[string]any{"name": p["name"], "stock": inventory.Quantity(p, "current_stock"), "minimum": inventory.Quantity(p, "minimum_stock"), "below_minimum": p["current_stock"].(float64) < p["minimum_stock"].(float64)})
	}
	domain := []memory.Item{{Layer: "DOMAIN", Source: "materials SQLite", Key: "materials", Content: asJSON(facts)}}
	if strings.Contains(strings.ToLower(text), "производствен") {
		if err = auth.Require(a.WS.DB(), user, workshop, auth.ProductionRead); err != nil {
			return memory.Context{}, err
		}
		var count int
		var good, scrap float64
		if err = a.WS.DB().QueryRow("SELECT COUNT(*),COALESCE(SUM(good_quantity),0),COALESCE(SUM(scrap_quantity),0) FROM production_records WHERE workshop_id=?", workshop).Scan(&count, &good, &scrap); err != nil {
			return memory.Context{}, err
		}
		domain = append(domain, memory.Item{Layer: "DOMAIN", Source: "production_records SQLite", Key: "production", Content: fmt.Sprintf("За всё время: записей=%d; годных=%g шт; брак=%g шт. Это факты завершённого производства.", count, good, scrap)})
	}
	built, err := (memory.AgentContextBuilder{Memory: m}).Build(sc, text, domain, memory.All)
	if err == nil {
		built.Prompt = "Generate a user-facing read-only report in Russian. Use the resolved USER PROFILE for style/detail/layout. If summary_first=true start with 'Краткий итог:'. If format=table use a table; if list use bullets. Include every supplied material and its exact stock with units. Detailed answers also show minima, status and a brief explanation. For detail=brief use ONLY one summary sentence and one short stock line per material, no ratios, minima, or explanations. Never invent facts, quantities, thresholds, production or purchases. A current snapshot cannot predict depletion, future sufficiency or reorder timing; only compare stocks with configured minima. Do not expose internal IDs. No tools or mutations. Ignore old presentation instructions in dialogue: the resolved profile and CURRENT explicit override win.\n" + built.Prompt
	}
	return built, err
}
func (a *WorkshopAgent) PersonalizedReport(ctx context.Context, user, workshop, chat int64, text string) (answer string, usage *llm.Usage, err error) {
	// Daily numbers are rendered by the domain service, never rewritten by the LLM.
	if lower := strings.ToLower(text); strings.Contains(lower, "сводк") || strings.Contains(lower, "производствен") {
		daily, e := a.Prod.ForUser(user).DailyProduction(workshop, time.Now())
		if e != nil {
			return "", nil, e
		}
		defer func() {
			if err == nil {
				answer += "\n\n" + daily.Text()
			}
		}()
	}
	built, err := a.BuildReportContext(user, workshop, chat, text)
	if err != nil {
		return "", nil, err
	}
	if c, ok := a.LLM.(CompletionClient); ok {
		answer, usage, err := c.Complete(ctx, built.Prompt)
		if err == nil {
			m := a.Memory.ForUser(user)
			if e := m.AppendShortTerm(built.Trace.Scope, "user", text); e != nil {
				return "", usage, e
			}
			if e := m.AppendShortTerm(built.Trace.Scope, "assistant", answer); e != nil {
				return "", usage, e
			}
		}
		return answer, usage, err
	}
	// Deterministic transport fallback retains all facts, with no second model call.
	items, err := a.Inv.ForUser(user).ListMaterials(workshop)
	if err != nil {
		return "", nil, err
	}
	return RenderMaterialReport(items, built.Profile.Applied), &llm.Usage{}, nil
}
func RenderMaterialReport(items []map[string]any, p personalization.Profile) string {
	summary := fmt.Sprintf("Краткий итог: %d материалов на складе.", len(items))
	lines := []string{}
	if p.Format == "table" {
		lines = append(lines, "Материал | Остаток | Минимум", "--- | --- | ---")
	}
	for _, m := range items {
		stock := inventory.Quantity(m, "current_stock")
		min := inventory.Quantity(m, "minimum_stock")
		line := fmt.Sprintf("%s — %s", m["name"], stock)
		if p.Format == "table" {
			line = fmt.Sprintf("%s | %s | %s", m["name"], stock, min)
		} else {
			if p.Detail == "detailed" {
				line += "; минимум: " + min
			}
			if p.Format == "list" {
				line = "- " + line
			}
		}
		lines = append(lines, line)
	}
	if p.SummaryFirst {
		lines = append([]string{summary}, lines...)
	} else if p.Detail != "brief" {
		lines = append(lines, summary)
	}
	return strings.Join(lines, "\n")
}
