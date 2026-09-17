package telegram

import (
	"strings"
	"testing"
)

func TestMaterialReferenceUsesShownIDsAndFreshStock(t *testing.T) {
	h := botFixture(t)
	h.bot.Agent = nil
	h.message(t, 900001, "/start")
	w, err := h.bot.WS.CreateOwnedWorkshop(1, "A")
	if err != nil {
		t.Fatal(err)
	}
	inv := h.bot.Inv.ForUser(1)
	if _, err = inv.CreateMaterial(w, "Гипс", "raw", "kg", 10, 0, "", 0, ""); err != nil {
		t.Fatal(err)
	}
	id, err := inv.CreateMaterial(w, "Кисточки", "component", "pcs", 400, 0, "", 0, "")
	if err != nil {
		t.Fatal(err)
	}
	h.message(t, 900001, "сколько второго?")
	if !strings.Contains(h.sent[len(h.sent)-1]["text"].(string), "/materials") {
		t.Fatal("invented reference without menu")
	}
	h.message(t, 900001, "материалы")
	if _, err = inv.CreateMaterial(w, "AAA", "raw", "g", 1, 0, "", 0, ""); err != nil {
		t.Fatal(err)
	}
	if _, err = inv.AdjustStockByID(w, id, 50); err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{"сколько второго?", " Сколько 2? ", "а сколько второго материала осталось?", "сколько материала №2?"} {
		h.message(t, 900001, input)
		if got := h.sent[len(h.sent)-1]["text"].(string); got != "Кисточки — 450 шт." {
			t.Fatal(got)
		}
	}
	h.message(t, 900001, "сколько первого?")
	if got := h.sent[len(h.sent)-1]["text"].(string); got != "Гипс — 10 кг." {
		t.Fatal(got)
	}
	h.message(t, 900001, "сколько 99?")
	if !strings.Contains(h.sent[len(h.sent)-1]["text"].(string), "Такого номера нет") {
		t.Fatal("invalid number")
	}
	if _, err = h.bot.WS.CreateOwnedWorkshop(1, "B"); err != nil {
		t.Fatal(err)
	}
	h.message(t, 900001, "сколько второго?")
	if !strings.Contains(h.sent[len(h.sent)-1]["text"].(string), "/materials") {
		t.Fatal("cross workshop reference")
	}
	if err = h.bot.WS.SetActiveWorkshop(1, w); err != nil {
		t.Fatal(err)
	}
	h.message(t, 900001, "/materials")
	h.bot.invalidateClear(sessionKey{900001, 1}, true)
	h.message(t, 900001, "сколько второго?")
	if !strings.Contains(h.sent[len(h.sent)-1]["text"].(string), "/materials") {
		t.Fatal("reference survived clear")
	}
}
