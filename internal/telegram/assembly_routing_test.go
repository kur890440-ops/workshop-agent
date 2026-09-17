package telegram

import (
	"strings"
	"testing"
)

func TestAssemblyOrdersDoNotRouteToPurchases(t *testing.T) {
	h, s, w := semanticFixture(t)
	s.fail = true
	// Equal minimum is not a positive purchasing requirement.
	id, err := h.bot.Inv.ForUser(1).CreateMaterial(w, "Упаковка", "raw", "pcs", 100, 100, "", 0, "")
	if err != nil {
		t.Fatal(err)
	}
	h.message(t, 900001, "соберем 5 заказов")
	if !strings.Contains(lastAnswer(h), "В мастерской пока нет товаров") || strings.Contains(lastAnswer(h), "Нужно заказать") {
		t.Fatal(lastAnswer(h))
	}
	if s.calls != 0 {
		t.Fatal("explicit assembly used LLM purchase route")
	}
	needs, err := h.bot.Inv.ForUser(1).ListPurchaseNeeds(w)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range needs {
		if item["id"] == id || item["order_quantity"].(float64) <= 0 {
			t.Fatal("zero purchase", item)
		}
	}
	h.message(t, 900001, "что нужно заказать?")
	if !strings.Contains(lastAnswer(h), "Закупка не требуется") {
		t.Fatal(lastAnswer(h))
	}
}
