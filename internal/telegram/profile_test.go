package telegram

import (
	"strings"
	"testing"
	"workshop-agent/internal/personalization"
)

func TestProfileUIWithoutWorkshopAndUserIsolation(t *testing.T) {
	h := botFixture(t)
	h.message(t, 900001, "/profile")
	requireAnswer(t, h, "Мой профиль")
	h.click(t, 900001, "Подробность")
	h.click(t, 900001, "Кратко")
	p, err := personalization.New(h.bot.WS.DB()).ForUser(1).GetProfile()
	if err != nil || p.Detail != "brief" {
		t.Fatal(p, err)
	}
	h.message(t, 900002, "/profile")
	requireAnswer(t, h, "Подробность: Обычно")
	h.message(t, 900001, "Теперь всегда отвечай подробно.")
	requireAnswer(t, h, "Подробность: Подробно")
	h.click(t, 900001, "Сбросить профиль")
	h.click(t, 900001, "Отмена")
	requireAnswer(t, h, "Подробность: Подробно")
	h.click(t, 900001, "Сбросить профиль")
	h.click(t, 900001, "Сбросить")
	requireAnswer(t, h, "Подробность: Обычно")
}
func TestSemanticReceivesProfileAndRequiredConfirmation(t *testing.T) {
	h, stub, w := semanticFixture(t)
	svc := personalization.New(h.bot.WS.DB()).ForUser(1)
	for k, v := range map[string]string{"response_style": "concise", "confirmation_level": "minimal_confirmation", "hide_llm_details": "true"} {
		if err := svc.UpdatePreference(k, v, "test"); err != nil {
			t.Fatal(err)
		}
	}
	stub.raw = `{"action":"get_material_stock","reference":{"kind":"name","entity_type":"material","name":"гипса"}}`
	h.message(t, 900001, "сколько гипса?")
	if !strings.Contains(stub.context, "[USER PROFILE]") || !strings.Contains(stub.context, "Detail level: brief") {
		t.Fatal("semantic profile missing")
	}
	if strings.Contains(lastAnswer(h), "Токены LLM:") {
		t.Fatal("ignored hide_llm_details")
	}
	h.message(t, 900001, "/profile trace")
	requireAnswer(t, h, "brief")
	stub.raw = `{"action":"change_material_stock","reference":{"kind":"name","entity_type":"material","name":"гипса"},"quantity_mode":"increase","amount":2,"unit":"kg"}`
	h.message(t, 900001, "добавь 2 кг гипса")
	item, err := h.bot.Inv.ForUser(1).MaterialByName(w, "Гипс")
	if err != nil || item["current_stock"] != float64(10000) {
		t.Fatal("minimal_confirmation bypassed confirmation", item, err)
	}
}
