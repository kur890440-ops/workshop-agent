package telegram

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"workshop-agent/internal/auth"
	"workshop-agent/internal/llm"
)

type semanticStub struct {
	raw     string
	fail    bool
	calls   int
	context string
}

func (s *semanticStub) ParseCommand(context.Context, string) (*llm.StructuredCommand, *llm.Usage, error) {
	return &llm.StructuredCommand{Action: "clarification"}, nil, nil
}
func (s *semanticStub) Interpret(_ context.Context, ctx, text string) (*llm.StructuredCommand, *llm.Usage, error) {
	s.calls++
	s.context = ctx
	if s.fail {
		return nil, nil, errors.New("offline")
	}
	c, e := llm.DecodeSemantic(s.raw)
	return c, &llm.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15}, e
}
func semanticFixture(t *testing.T) (*harness, *semanticStub, int64) {
	h := botFixture(t)
	h.message(t, 900001, "/start")
	w, err := h.bot.WS.CreateOwnedWorkshop(1, "A")
	if err != nil {
		t.Fatal(err)
	}
	inv := h.bot.Inv.ForUser(1)
	for _, m := range []struct {
		name, unit string
		stock, min float64
	}{{"Гипс", "kg", 10, 2}, {"Кисточки", "pcs", 400, 50}, {"Коробки", "pcs", 100, 20}} {
		if _, err = inv.CreateMaterial(w, m.name, "raw", m.unit, m.stock, m.min, "", 0, ""); err != nil {
			t.Fatal(err)
		}
	}
	stub := &semanticStub{}
	h.bot.Agent.LLM = stub
	h.message(t, 900001, "материалы")
	return h, stub, w
}
func lastAnswer(h *harness) string { return h.sent[len(h.sent)-1]["text"].(string) }
func TestSemanticParaphrasesAndFreshDomain(t *testing.T) {
	h, s, w := semanticFixture(t)
	for _, text := range []string{"какой остаток 2", "остаток второго материала", "покажи остаток позиции 2", "сколько по второму пункту?", "а второго?"} {
		s.raw = `{"action":"get_material_stock","reference":{"kind":"list_position","entity_type":"material","position":2}}`
		h.message(t, 900001, text)
		if !strings.Contains(lastAnswer(h), "Кисточки — 400 шт.") {
			t.Fatal(lastAnswer(h))
		}
	}
	for _, mention := range []string{"кисточек", "кисточки", "кисточик"} {
		s.raw = `{"action":"get_material_stock","reference":{"kind":"name","entity_type":"material","name":"` + mention + `"}}`
		h.message(t, 900001, "сколько осталось "+mention+"?")
		if !strings.Contains(lastAnswer(h), "400 шт") {
			t.Fatal(lastAnswer(h))
		}
	}
	s.raw = `{"action":"get_material_minimum","reference":{"kind":"last","entity_type":"material"}}`
	h.message(t, 900001, "а его минимум?")
	if !strings.Contains(lastAnswer(h), "50 шт") {
		t.Fatal(lastAnswer(h))
	}
	inv := h.bot.Inv.ForUser(1)
	id, err := inv.GetMaterialID(w, "Кисточки")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = inv.AdjustStockByID(w, id, 5); err != nil {
		t.Fatal(err)
	}
	if _, err = inv.CreateMaterial(w, "AAA", "raw", "g", 1, 0, "", 0, ""); err != nil {
		t.Fatal(err)
	}
	s.raw = `{"action":"get_material_stock","reference":{"kind":"list_position","entity_type":"material","position":2}}`
	h.message(t, 900001, "какой остаток 2")
	if !strings.Contains(lastAnswer(h), "405 шт") {
		t.Fatal(lastAnswer(h))
	}
	h.message(t, 900001, "сколько второго?")
	if lastAnswer(h) != "Кисточки — 405 шт." {
		t.Fatal(lastAnswer(h))
	}
	if !strings.Contains(s.context, `"list_ids"`) {
		t.Fatal("UI context missing")
	}
}
func TestSemanticAmbiguityAndContextReset(t *testing.T) {
	h, s, w := semanticFixture(t)
	if _, err := h.bot.Inv.ForUser(1).CreateMaterial(w, "Гипс белый", "raw", "kg", 5, 0, "", 0, ""); err != nil {
		t.Fatal(err)
	}
	s.raw = `{"action":"get_material_stock","reference":{"kind":"name","entity_type":"material","name":"гипса"}}`
	h.message(t, 900001, "сколько гипса?")
	if !strings.Contains(lastAnswer(h), "несколько материалов") {
		t.Fatal(lastAnswer(h))
	}
	s.raw = `{"action":"get_material_stock","reference":{"kind":"list_position","entity_type":"material","position":99}}`
	h.message(t, 900001, "какой остаток 99")
	if !strings.Contains(lastAnswer(h), "Такого номера нет") {
		t.Fatal(lastAnswer(h))
	}
	s.raw = `{"action":"get_material_stock","reference":{"kind":"list_position","entity_type":"material","position":2}}`
	for _, reset := range []string{"/products", "/session new"} {
		h.message(t, 900001, "/materials")
		h.message(t, 900001, reset)
		h.message(t, 900001, "какой остаток 2")
		if !strings.Contains(lastAnswer(h), "/materials") {
			t.Fatal(lastAnswer(h))
		}
	}
	h.message(t, 900001, "/materials")
	h.bot.invalidateClear(sessionKey{900001, 1}, true)
	h.message(t, 900001, "какой остаток 2")
	if !strings.Contains(lastAnswer(h), "/materials") {
		t.Fatal(lastAnswer(h))
	}
	h.message(t, 900001, "/materials")
	if _, err := h.bot.WS.CreateOwnedWorkshop(1, "Other"); err != nil {
		t.Fatal(err)
	}
	h.message(t, 900001, "какой остаток 2")
	if !strings.Contains(lastAnswer(h), "/materials") {
		t.Fatal(lastAnswer(h))
	}
}
func TestSemanticWritesRequireConfirmation(t *testing.T) {
	h, s, w := semanticFixture(t)
	for _, tc := range []struct {
		mode, text, amount string
		expected           float64
	}{{"absolute", "установи остаток гипса 12 кг", "12", 12000}, {"increase", "добавь 2 кг гипса на склад", "2", 14000}, {"decrease", "спиши 0,5 кг гипса", "0.5", 13500}} {
		before, _ := h.bot.Inv.ForUser(1).GetMaterialStock(w, "Гипс")
		s.raw = `{"action":"change_material_stock","reference":{"kind":"name","entity_type":"material","name":"гипса"},"amount":` + tc.amount + `,"quantity_mode":"` + tc.mode + `","unit":"kg"}`
		h.message(t, 900001, tc.text)
		got, _ := h.bot.Inv.ForUser(1).GetMaterialStock(w, "Гипс")
		if got != before {
			t.Fatal("write before confirmation")
		}
		h.click(t, 900001, "Подтвердить")
		got, _ = h.bot.Inv.ForUser(1).GetMaterialStock(w, "Гипс")
		if got != tc.expected {
			t.Fatal(lastAnswer(h), got)
		}
	}
	h.message(t, 900001, "спиши 0,5 кг гипса")
	h.click(t, 900001, "Отмена")
	h.click(t, 900001, "Подтвердить")
	got, _ := h.bot.Inv.ForUser(1).GetMaterialStock(w, "Гипс")
	if got != 13500 {
		t.Fatal("cancel did not invalidate")
	}
	// Permission changes after proposal must still prevent the write.
	h.message(t, 900002, "/start")
	var user int64
	if err := h.bot.WS.DB().QueryRow(`SELECT id FROM users WHERE telegram_user_id=900002`).Scan(&user); err != nil {
		t.Fatal(err)
	}
	_, token, err := h.bot.WS.CreateInvite(1, w, auth.Employee, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.bot.WS.AcceptInvite(user, token); err != nil {
		t.Fatal(err)
	}
	h.message(t, 900002, "спиши 0,5 кг гипса")
	if err = h.bot.WS.ChangeRole(1, w, user, auth.Viewer); err != nil {
		t.Fatal(err)
	}
	h.click(t, 900002, "Подтвердить")
	got, _ = h.bot.Inv.ForUser(1).GetMaterialStock(w, "Гипс")
	if got != 13500 {
		t.Fatal("revoked permission ignored")
	}
}
func TestSemanticFailureAndFormPriority(t *testing.T) {
	h, s, w := semanticFixture(t)
	for _, raw := range []string{"broken", `{"action":"get_material_stock","material_id":1}`, `{"action":"erase_database"}`} {
		s.raw = raw
		h.message(t, 900001, "какой остаток 2")
		if !strings.Contains(lastAnswer(h), "Не удалось") {
			t.Fatal(lastAnswer(h))
		}
	}
	s.fail = true
	h.message(t, 900001, "какой остаток 2")
	if !strings.Contains(lastAnswer(h), "/materials") {
		t.Fatal(lastAnswer(h))
	}
	calls := s.calls
	h.message(t, 900001, "/materials")
	h.click(t, 900001, "Редактировать")
	h.message(t, 900001, "1")
	h.message(t, 900001, "2")
	if s.calls != calls {
		t.Fatal("form called LLM")
	}
	stock, err := h.bot.Inv.ForUser(1).GetMaterialStock(w, "Гипс")
	if err != nil || stock != 2000 {
		t.Fatal(stock, err)
	}
}

func TestSemanticContextExpiryAndUserIsolation(t *testing.T) {
	h, s, w := semanticFixture(t)
	s.raw = `{"action":"get_material_stock","reference":{"kind":"list_position","entity_type":"material","position":2}}`
	h.message(t, 900002, "/start")
	var user int64
	if err := h.bot.WS.DB().QueryRow(`SELECT id FROM users WHERE telegram_user_id=900002`).Scan(&user); err != nil {
		t.Fatal(err)
	}
	_, token, err := h.bot.WS.CreateInvite(1, w, auth.Viewer, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.bot.WS.AcceptInvite(user, token); err != nil {
		t.Fatal(err)
	}
	h.message(t, 900002, "какой остаток 2")
	if !strings.Contains(lastAnswer(h), "/materials") {
		t.Fatal("user reference leaked", lastAnswer(h))
	}
	h.bot.uiMu.Lock()
	v := h.bot.materialLists[sessionKey{900001, 1}]
	v.Expires = time.Now().Add(-time.Second)
	h.bot.materialLists[sessionKey{900001, 1}] = v
	h.bot.uiMu.Unlock()
	h.message(t, 900001, "какой остаток 2")
	if !strings.Contains(lastAnswer(h), "/materials") {
		t.Fatal("expired reference survived")
	}
	h.message(t, 900001, "/materials")
	// Even an external session rotation invalidates the in-memory UI snapshot.
	if _, err = h.bot.WS.DB().Exec(`UPDATE conversation_sessions SET status='closed' WHERE user_id=1`); err != nil {
		t.Fatal(err)
	}
	h.message(t, 900001, "какой остаток 2")
	if !strings.Contains(lastAnswer(h), "/materials") {
		t.Fatal("old session reference survived")
	}
}

func TestSemanticConfirmationRejectsNewSession(t *testing.T) {
	h, s, w := semanticFixture(t)
	s.raw = `{"action":"change_material_stock","reference":{"kind":"name","entity_type":"material","name":"гипса"},"amount":2,"quantity_mode":"increase","unit":"kg"}`
	h.message(t, 900001, "добавь 2 кг гипса на склад")
	if _, err := h.bot.WS.DB().Exec(`UPDATE conversation_sessions SET status='closed' WHERE user_id=1`); err != nil {
		t.Fatal(err)
	}
	h.click(t, 900001, "Подтвердить")
	stock, err := h.bot.Inv.ForUser(1).GetMaterialStock(w, "Гипс")
	if err != nil || stock != 10000 {
		t.Fatal("old confirmation changed stock", stock, err)
	}
}
