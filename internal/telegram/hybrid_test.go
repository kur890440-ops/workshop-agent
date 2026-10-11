package telegram

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
	"workshop-agent/internal/llm"
)

type hybridProviderStub struct {
	route, command, answer string
	failure                error
	calls, semantic        int
	prompts                []string
}

func (s *hybridProviderStub) ModelName() string { return "synthetic-qwen" }
func (s *hybridProviderStub) Generate(_ context.Context, p string, _ bool, _ int) (string, *llm.Usage, error) {
	s.calls++
	s.prompts = append(s.prompts, p)
	if strings.HasPrefix(p, llm.SemanticInstructions) {
		s.semantic++
		return s.command, nil, s.failure
	}
	if strings.HasPrefix(p, "Classify CURRENT") {
		return s.route, nil, s.failure
	}
	return s.answer, nil, s.failure
}
func hybridFixture(t *testing.T) (*harness, *hybridProviderStub, *hybridProviderStub, int64) {
	h, _, w := semanticFixture(t)
	local := &hybridProviderStub{route: `{"route":"GENERAL","reason":"GENERAL_KNOWLEDGE","complexity":"SIMPLE","general_answer":"PETG — термопластичный полимер."}`, command: `{"action":"get_all_material_stock"}`}
	cloud := &hybridProviderStub{answer: "Облачный ответ по зафиксированному результату", command: `{"action":"get_all_material_stock"}`}
	h.bot.Hybrid = &llm.HybridClient{Local: local, Cloud: cloud, Policy: llm.LLMFallbackPolicy{Mode: llm.Auto}}
	h.bot.Agent.LLM = h.bot.Hybrid
	return h, local, cloud, w
}
func TestHybridGeneralSharedAdaptersAndRecheck(t *testing.T) {
	h, local, cloud, _ := hybridFixture(t)
	web := connectWeb(t, h)
	h.message(t, 900001, "Что такое PETG?")
	if local.calls != 1 || local.semantic != 0 || cloud.calls != 0 {
		t.Fatal("general entered workshop/cloud")
	}
	for _, p := range local.prompts {
		if strings.Contains(p, "Кисточки") {
			t.Fatal("hidden catalog leaked to router")
		}
	}
	h.click(t, 900001, "Перепроверить через облачную LLM")
	if cloud.calls != 0 {
		t.Fatal("recheck lacks consent")
	}
	h.click(t, 900001, "Разрешить внешнюю обработку")
	if cloud.calls != 1 || local.semantic != 0 {
		t.Fatal("recheck executed tools/planning")
	}
	h.click(t, 900001, "Разрешить внешнюю обработку")
	if cloud.calls != 1 {
		t.Fatal("double click repeated cloud")
	}
	web.post(t, "/api/chat/messages", "Что такое PETG?")
	out := web.history(t)
	if len(out) < 2 || !strings.Contains(out[len(out)-2].Text, "PETG") {
		t.Fatal(out)
	}
	web.click(t, "Перепроверить через облачную LLM")
	web.click(t, "Разрешить внешнюю обработку")
	if cloud.calls != 2 || local.semantic != 0 {
		t.Fatal("web differs")
	}
	var count int
	if e := h.bot.WS.DB().QueryRow("SELECT count(*) FROM hybrid_llm_runs").Scan(&count); e != nil || count < 4 {
		t.Fatal("diagnostics", count, e)
	}
}
func TestHybridConsentContinuesPlanningOnceAndPreservesConfirmation(t *testing.T) {
	h, local, cloud, w := hybridFixture(t)
	local.route = `{"route":"WORKSHOP","reason":"WORKSHOP_ACTION","complexity":"SIMPLE","general_answer":""}`
	local.command = "invalid JSON"
	cloud.command = `{"action":"change_material_stock","reference":{"kind":"name","entity_type":"material","name":"кисточек"},"amount":5,"quantity_mode":"decrease","unit":"pcs"}`
	h.message(t, 900001, "Спиши 5 кисточек")
	if cloud.calls != 0 {
		t.Fatal("cloud without consent")
	}
	inv := h.bot.Inv.ForUser(1)
	item, _ := inv.MaterialByName(w, "Кисточки")
	if item["current_stock"].(float64) != 400 {
		t.Fatal("premature write")
	}
	h.click(t, 900001, "Разрешить внешнюю обработку")
	item, _ = inv.MaterialByName(w, "Кисточки")
	if item["current_stock"].(float64) != 400 || cloud.calls != 1 {
		t.Fatal("consent executed domain write")
	}
	h.click(t, 900001, "Подтвердить")
	item, _ = inv.MaterialByName(w, "Кисточки")
	if item["current_stock"].(float64) != 395 {
		t.Fatal("normal confirmation failed", item)
	}
	h.click(t, 900001, "Подтвердить")
	item, _ = inv.MaterialByName(w, "Кисточки")
	if item["current_stock"].(float64) != 395 || cloud.calls != 1 {
		t.Fatal("duplicate side effect")
	}
}
func TestHybridLocalOnlyAndUnauthorized(t *testing.T) {
	h, local, cloud, _ := hybridFixture(t)
	h.bot.Hybrid.Policy.Mode = llm.LocalOnly
	local.failure = errors.New("offline")
	h.message(t, 900001, "Что такое PETG?")
	if cloud.calls != 0 {
		t.Fatal("LOCAL_ONLY cloud")
	}
	before := local.calls
	h.message(t, 900002, "Отмени заказ 123")
	if local.calls != before || cloud.calls != 0 {
		t.Fatal("unauthorized reached provider")
	}
}
func TestHybridConsentInvalidatedByNewInput(t *testing.T) {
	h, local, cloud, _ := hybridFixture(t)
	h.message(t, 900001, "Что такое PETG?")
	h.click(t, 900001, "Перепроверить через облачную LLM")
	local.route = `{"route":"GENERAL","reason":"GENERAL_KNOWLEDGE","complexity":"SIMPLE","general_answer":"ABS — пластик."}`
	h.message(t, 900001, "Что такое ABS?")
	h.click(t, 900001, "Разрешить внешнюю обработку")
	if cloud.calls != 0 {
		t.Fatal("stale context approval")
	}
}

func TestHybridLiveLocalApplication(t *testing.T) {
	if os.Getenv("HYBRID_LIVE_LOCAL") != "1" {
		t.Skip("requires real Ollama, uses isolated synthetic database")
	}
	h, _, cloud, w := hybridFixture(t)
	local, e := llm.NewOllamaClient("http://127.0.0.1:11434", "", 120*time.Second)
	if e != nil {
		t.Fatal(e)
	}
	if e = local.DiscoverModel(context.Background()); e != nil {
		t.Fatal(e)
	}
	h.bot.Hybrid.Local = local
	h.message(t, 900001, "Что такое PETG?")
	if !strings.Contains(lastAnswer(h), "Qwen") || cloud.calls != 0 {
		t.Fatal("general failed")
	}
	h.message(t, 900001, "Сколько сейчас кисточек в нашей мастерской?")
	var found bool
	for _, msg := range h.sent {
		if text, ok := msg["text"].(string); ok && strings.Contains(text, "400") {
			found = true
		}
	}
	if !found || cloud.calls != 0 {
		t.Fatal("real local workshop read failed")
	}
	h.message(t, 900001, "Спиши 5 кисточек")
	item, e := h.bot.Inv.ForUser(1).MaterialByName(w, "Кисточки")
	if e != nil {
		t.Fatal(e)
	}
	if item["current_stock"].(float64) != 400 {
		t.Fatal("write without confirmation")
	}
	h.click(t, 900001, "Подтвердить")
	item, e = h.bot.Inv.ForUser(1).MaterialByName(w, "Кисточки")
	if e != nil || item["current_stock"].(float64) != 395 {
		t.Fatal("confirmed local command failed", e)
	}
	t.Log("real Qwen general + workshop read + normal action confirmation passed; isolated DB and simulated Telegram transport, no live MCP")
}

func TestHybridConsentChecksRoleSessionAndExpiry(t *testing.T) {
	for _, change := range []string{"role", "session", "expiry"} {
		t.Run(change, func(t *testing.T) {
			h, _, cloud, w := hybridFixture(t)
			h.message(t, 900001, "Что такое PETG?")
			h.click(t, 900001, "Перепроверить через облачную LLM")
			switch change {
			case "role":
				if _, e := h.bot.WS.DB().Exec("UPDATE workshop_members SET role='VIEWER' WHERE user_id=1 AND workshop_id=?", w); e != nil {
					t.Fatal(e)
				}
			case "session":
				if _, e := h.bot.WS.DB().Exec("UPDATE conversation_sessions SET status='closed' WHERE user_id=1"); e != nil {
					t.Fatal(e)
				}
			case "expiry":
				for id, p := range h.bot.hybridPending {
					p.Expires = time.Now().Add(-time.Second)
					h.bot.hybridPending[id] = p
				}
			}
			h.click(t, 900001, "Разрешить внешнюю обработку")
			if cloud.calls != 0 {
				t.Fatal("stale approval disclosed context")
			}
		})
	}
}
func TestHybridCloudOnlyAndDiagnosticsPrivacy(t *testing.T) {
	h, local, cloud, _ := hybridFixture(t)
	h.bot.Hybrid.Policy.Mode = llm.CloudOnly
	question := "Объясни PETG для synthetic-private-customer"
	h.message(t, 900001, question)
	if local.calls != 1 || local.semantic != 0 || cloud.calls != 0 {
		t.Fatal("cloud only must route locally then require consent")
	}
	h.click(t, 900001, "Разрешить внешнюю обработку")
	if cloud.calls != 1 {
		t.Fatal("cloud only not completed")
	}
	rows, e := h.bot.WS.DB().Query("SELECT metadata FROM hybrid_llm_runs")
	if e != nil {
		t.Fatal(e)
	}
	defer rows.Close()
	for rows.Next() {
		var data string
		if e = rows.Scan(&data); e != nil {
			t.Fatal(e)
		}
		if strings.Contains(data, "synthetic-private-customer") {
			t.Fatal("payload leaked into diagnostics")
		}
	}
}
