package telegram

import (
	"strings"
	"testing"
	"workshop-agent/internal/inventory"
)

func TestDisplayUnitsMenuAndStock(t *testing.T) {
	h := botFixture(t)
	h.bot.Agent = nil
	h.message(t, 900001, "/start")
	w, err := h.bot.WS.CreateOwnedWorkshop(1, "A")
	if err != nil {
		t.Fatal(err)
	}
	inv := h.bot.Inv.ForUser(1)
	id, err := inv.CreateMaterial(w, "Гипс", "сырьё", "g", 10000, 12000, "", 0, "")
	if err != nil {
		t.Fatal(err)
	}
	h.message(t, 900001, "/materials")
	h.click(t, 900001, "Единица измерения")
	h.message(t, 900001, "1")
	h.click(t, 900001, "кг")
	h.click(t, 900001, "Подтвердить")
	if !strings.Contains(h.sent[len(h.sent)-1]["text"].(string), "10 кг") {
		t.Fatal("menu did not convert")
	}
	var stock, minimum float64
	var base, unit string
	if err = inv.DB().QueryRow(`SELECT current_stock,minimum_stock,base_unit,display_unit FROM materials WHERE id=?`, id).Scan(&stock, &minimum, &base, &unit); err != nil || stock != 10000 || minimum != 12000 || base != "g" || unit != "kg" {
		t.Fatalf("storage changed %g %g %s %s %v", stock, minimum, base, unit, err)
	}
	var count int
	if err = inv.DB().QueryRow(`SELECT COUNT(*) FROM inventory_movements WHERE material_id=?`, id).Scan(&count); err != nil || count != 0 {
		t.Fatal("unit change created movement")
	}
	if err = inv.DB().QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE entity_id=? AND actor_user_id=1 AND field_name='display_unit' AND old_value='g' AND new_value='kg'`, id).Scan(&count); err != nil || count != 1 {
		t.Fatal("missing unit audit", err)
	}
	for _, cmd := range []string{"/stock Гипс", "/to_order"} {
		h.message(t, 900001, cmd)
		if !strings.Contains(h.sent[len(h.sent)-1]["text"].(string), "кг") {
			t.Fatal("unit missing", cmd)
		}
	}
	needs, err := inv.ListPurchaseNeeds(w)
	if err != nil || len(needs) != 1 {
		t.Fatal(err)
	}
	if inventory.Quantity(needs[0], "order_quantity") != "2 кг" {
		t.Fatal("purchase conversion")
	}
	for _, tc := range []struct {
		input string
		stock float64
	}{{"10", 10000}, {"+2", 12000}, {"-0,5", 11500}, {"0", 0}, {"0,001", 1}} {
		h.message(t, 900001, "/materials")
		h.click(t, 900001, "Редактировать")
		h.message(t, 900001, "1")
		h.message(t, 900001, tc.input)
		actual, err := inv.GetMaterialStock(w, "Гипс")
		if err != nil || actual != tc.stock {
			t.Fatalf("%s: %g %v", tc.input, actual, err)
		}
	}
	h.message(t, 900001, "/materials")
	h.click(t, 900001, "Редактировать")
	h.message(t, 900001, "1")
	if err = inv.SetDisplayUnit(w, id, "g"); err != nil {
		t.Fatal(err)
	}
	h.message(t, 900001, "10")
	if !strings.Contains(h.sent[len(h.sent)-1]["text"].(string), "единица изменилась") {
		t.Fatal("stale unit accepted")
	}
	actual, err := inv.GetMaterialStock(w, "Гипс")
	if err != nil || actual != 1 {
		t.Fatal("stale input changed stock")
	}
	h.message(t, 900001, "1")
	h.message(t, 900001, "10")
	actual, err = inv.GetMaterialStock(w, "Гипс")
	if err != nil || actual != 10 {
		t.Fatal("new unit not applied")
	}
	h.message(t, 900001, "/setup_stop")
	h.message(t, 900001, "/materials")
	h.click(t, 900001, "Добавить")
	for _, input := range []string{"Новый гипс", "1", "2", "10", "2", "да"} {
		h.message(t, 900001, input)
	}
	item, err := inv.MaterialByName(w, "Новый гипс")
	if err != nil || item["current_stock"] != float64(10000) || inventory.DisplayUnit(item) != "kg" {
		t.Fatal("creation conversion", item, err)
	}
}
