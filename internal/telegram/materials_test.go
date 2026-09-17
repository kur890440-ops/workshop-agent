package telegram

import (
	"strings"
	"testing"
	"unicode/utf16"
	"workshop-agent/internal/auth"
)

func TestMaterialsMenuAdjustment(t *testing.T) {
	h := botFixture(t)
	h.message(t, 900001, "/start")
	w, err := h.bot.WS.CreateOwnedWorkshop(1, "Test")
	if err != nil {
		t.Fatal(err)
	}
	id, err := h.bot.Inv.ForUser(1).CreateMaterial(w, "PETG", "сырьё", "g", 10, 1, "", 0, "")
	if err != nil {
		t.Fatal(err)
	}
	h.message(t, 900001, "/materials")
	h.click(t, 900001, "Редактировать")
	h.message(t, 900001, "99")
	h.message(t, 900001, "1")
	for _, input := range []string{"NaN", "+Inf", "-11", "+0", "-0", "abc"} {
		h.message(t, 900001, input)
	}
	h.message(t, 900001, "-2,5")
	var stock float64
	h.bot.Inv.DB().QueryRow(`SELECT current_stock FROM materials WHERE id=?`, id).Scan(&stock)
	if stock != 7.5 {
		t.Fatalf("stock=%g", stock)
	}
	var count int
	h.bot.Inv.DB().QueryRow(`SELECT COUNT(*) FROM inventory_movements WHERE material_id=?`, id).Scan(&count)
	if count != 1 {
		t.Fatalf("movements=%d", count)
	}
	h.click(t, 900001, "Добавить")
	for _, input := range []string{"Brush", "2", "5", "20", "5", "да"} {
		h.message(t, 900001, input)
	}
	if _, err := h.bot.Inv.ForUser(1).GetMaterialID(w, "Brush"); err != nil {
		t.Fatal(err)
	}
}

func TestMaterialsMenuPlainTextAliases(t *testing.T) {
	h := botFixture(t)
	h.bot.Agent = nil
	h.message(t, 900001, "/start")
	w, err := h.bot.WS.CreateOwnedWorkshop(1, "A")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.bot.Inv.ForUser(1).CreateMaterial(w, "Гипс", "сырьё", "g", 40, 0, "", 0, ""); err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{"материалы", "остатки", "  МАТЕРИАЛЫ  ", "Остатки", "/materials"} {
		h.message(t, 900001, input)
		last := h.sent[len(h.sent)-1]
		text, _ := last["text"].(string)
		if !strings.Contains(text, "1. Гипс — 40 г") || strings.Contains(text, "Токены LLM") {
			t.Fatalf("unexpected response: %s", text)
		}
		rows := last["reply_markup"].(map[string]any)["inline_keyboard"].([]any)
		if len(rows) != 3 {
			t.Fatal("missing material menu actions")
		}
	}
	// A form's input must not be interpreted as a menu alias.
	h.click(t, 900001, "Добавить")
	h.message(t, 900001, "материалы")
	s := h.bot.getSetup(900001, 1)
	if s == nil || s.name != "материалы" || s.stage != 1 {
		t.Fatal("alias interrupted material form")
	}
}

func TestMaterialsMenuScopePermissionsAndCancellation(t *testing.T) {
	h := botFixture(t)
	h.bot.Agent = nil // Menu flows must work without any LLM/agent.
	h.message(t, 900001, "/start")
	w, err := h.bot.WS.CreateOwnedWorkshop(1, "A")
	if err != nil {
		t.Fatal(err)
	}
	inv := h.bot.Inv.ForUser(1)
	id, err := inv.CreateMaterial(w, "Zinc", "сырьё", "g", 10, 0, "", 0, "")
	if err != nil {
		t.Fatal(err)
	}
	h.message(t, 900001, "/materials")
	// New alphabetical entries cannot change the meaning of an already displayed number.
	if _, err = inv.CreateMaterial(w, "Aluminum", "сырьё", "g", 20, 0, "", 0, ""); err != nil {
		t.Fatal(err)
	}
	h.click(t, 900001, "Редактировать")
	h.message(t, 900001, "1")
	h.message(t, 900001, "+2.5")
	stock, err := inv.GetMaterialStock(w, "Zinc")
	if err != nil || stock != 12.5 {
		t.Fatalf("stable ID: %g %v", stock, err)
	}
	var actor int64
	if err = h.bot.Inv.DB().QueryRow(`SELECT user_id FROM inventory_movements WHERE material_id=?`, id).Scan(&actor); err != nil || actor != 1 {
		t.Fatalf("actor=%d %v", actor, err)
	}
	h.click(t, 900001, "Редактировать")
	h.message(t, 900001, "2")
	h.message(t, 900001, "/setup_stop")
	if h.bot.getSetup(900001, 1) != nil {
		t.Fatal("cancel did not clear state")
	}
	h.message(t, 900001, "/materials")
	h.click(t, 900001, "Добавить")
	h.message(t, 900001, "Cancelled")
	h.message(t, 900001, "/setup_stop")
	if _, err = inv.GetMaterialID(w, "Cancelled"); err == nil {
		t.Fatal("cancel created material")
	}
	h.message(t, 900001, "/materials")
	h.click(t, 900001, "Редактировать")
	other, err := h.bot.WS.CreateOwnedWorkshop(1, "B")
	if err != nil {
		t.Fatal(err)
	}
	h.message(t, 900001, "1")
	if h.bot.getSetup(900001, 1) != nil {
		t.Fatal("workshop switch retained input")
	}
	h.click(t, 900001, "Добавить") // Old A menu is invalid while B is active.
	if h.bot.getSetup(900001, 1) != nil {
		t.Fatal("old menu accepted")
	}
	if err = h.bot.WS.SetActiveWorkshop(1, w); err != nil {
		t.Fatal(err)
	}
	h.message(t, 900002, "/start")
	var member int64
	if err = h.bot.WS.DB().QueryRow(`SELECT id FROM users WHERE telegram_user_id=900002`).Scan(&member); err != nil {
		t.Fatal(err)
	}
	_, token, err := h.bot.WS.CreateInvite(1, w, auth.Employee, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.bot.WS.AcceptInvite(member, token); err != nil {
		t.Fatal(err)
	}
	h.message(t, 900002, "/materials")
	h.click(t, 900002, "Редактировать")
	h.message(t, 900002, "2")
	if err = h.bot.WS.ChangeRole(1, w, member, auth.Viewer); err != nil {
		t.Fatal(err)
	}
	h.message(t, 900002, "+5")
	stock, err = inv.GetMaterialStock(w, "Zinc")
	if err != nil || stock != 12.5 {
		t.Fatal("revoked write permission ignored")
	}
	h.message(t, 900002, "/materials")
	last := h.sent[len(h.sent)-1]
	rows := last["reply_markup"].(map[string]any)["inline_keyboard"].([]any)
	if len(rows) != 0 {
		t.Fatal("viewer has mutation buttons")
	}
	if _, err = h.bot.Inv.ForUser(member).AdjustStockByID(w, id, 1); err == nil {
		t.Fatal("viewer service write allowed")
	}
	if _, err = inv.AdjustStockByID(other, id, 1); err == nil {
		t.Fatal("cross-workshop material accepted")
	}
	if err = h.bot.WS.ChangeMemberStatus(1, w, member, "disabled"); err != nil {
		t.Fatal(err)
	}
	if _, err = h.bot.Inv.ForUser(member).ListMaterials(w); err == nil {
		t.Fatal("disabled member can read")
	}
}

func TestMaterialsMenuEmptyLongListAndNumericCreation(t *testing.T) {
	h := botFixture(t)
	h.bot.Agent = nil
	h.message(t, 900001, "/start")
	w, err := h.bot.WS.CreateOwnedWorkshop(1, "A")
	if err != nil {
		t.Fatal(err)
	}
	h.message(t, 900001, "/materials")
	if !strings.Contains(h.sent[len(h.sent)-1]["text"].(string), "Пока нет") {
		t.Fatal("empty state")
	}
	h.click(t, 900001, "Добавить")
	for _, v := range []string{"PETG", "1", "2", "NaN", "+Inf", "1e308", "1,5", "NaN", "0,5", "да"} {
		h.message(t, 900001, v)
	}
	stock, err := h.bot.Inv.ForUser(1).GetMaterialStock(w, "PETG")
	if err != nil || stock != 1500 {
		t.Fatalf("creation: %g %v", stock, err)
	}
	name := strings.Repeat("🧵", 4000)
	if _, err = h.bot.Inv.ForUser(1).CreateMaterial(w, name, "сырьё", "g", 1, 0, "", 0, ""); err != nil {
		t.Fatal(err)
	}
	start := len(h.sent)
	h.message(t, 900001, "/materials")
	var joined string
	for _, sent := range h.sent[start:] {
		text, _ := sent["text"].(string)
		if len(utf16.Encode([]rune(text))) > 4096 {
			t.Fatal("Telegram size limit exceeded")
		}
		joined += text
	}
	if !strings.Contains(joined, name) {
		t.Fatal("list lost long material name")
	}
}

func TestMaterialsAdjustmentRollsBackOnMovementFailure(t *testing.T) {
	h := botFixture(t)
	h.message(t, 900001, "/start")
	w, err := h.bot.WS.CreateOwnedWorkshop(1, "A")
	if err != nil {
		t.Fatal(err)
	}
	inv := h.bot.Inv.ForUser(1)
	id, err := inv.CreateMaterial(w, "PETG", "сырьё", "g", 10, 0, "", 0, "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = inv.DB().Exec(`CREATE TRIGGER fail_movement BEFORE INSERT ON inventory_movements BEGIN SELECT RAISE(ABORT,'test failure'); END`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = inv.AdjustStockByID(w, id, 5); err == nil {
		t.Fatal("expected failure")
	}
	if _, _, err = inv.SetStockByID(w, id, 10000); err == nil {
		t.Fatal("expected absolute update failure")
	}
	stock, err := inv.GetMaterialStock(w, "PETG")
	if err != nil || stock != 10 {
		t.Fatalf("rollback: %g %v", stock, err)
	}
}

func TestMaterialsAbsoluteAndRelativeInputs(t *testing.T) {
	h := botFixture(t)
	h.bot.Agent = nil
	h.message(t, 900001, "/start")
	w, err := h.bot.WS.CreateOwnedWorkshop(1, "A")
	if err != nil {
		t.Fatal(err)
	}
	inv := h.bot.Inv.ForUser(1)
	id, err := inv.CreateMaterial(w, "Гипс", "сырьё", "g", 50, 0, "", 0, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		input           string
		expected, delta float64
		message         string
	}{
		{" 10000 ", 10000, 9950, "остаток установлен"},
		{"+10000", 20000, 10000, "изменение +10000"},
		{"-20", 19980, -20, "изменение -20"},
		{"0", 0, -19980, "остаток установлен"},
		{"10,5", 10.5, 10.5, "остаток установлен"},
		{"+2.5", 13, 2.5, "изменение +2.5"},
		{"-1,5", 11.5, -1.5, "изменение -1.5"},
		{"11.5", 11.5, 0, "Остаток уже равен указанному значению"},
	} {
		h.message(t, 900001, "/materials")
		h.click(t, 900001, "Редактировать")
		h.message(t, 900001, "1")
		var before int
		if err = inv.DB().QueryRow(`SELECT COUNT(*) FROM inventory_movements WHERE material_id=?`, id).Scan(&before); err != nil {
			t.Fatal(err)
		}
		h.message(t, 900001, tc.input)
		stock, err := inv.GetMaterialStock(w, "Гипс")
		if err != nil || stock != tc.expected {
			t.Fatalf("%s: stock=%g err=%v", tc.input, stock, err)
		}
		if !strings.Contains(h.sent[len(h.sent)-2]["text"].(string), tc.message) {
			t.Fatalf("wrong response for %s", tc.input)
		}
		var after int
		if err = inv.DB().QueryRow(`SELECT COUNT(*) FROM inventory_movements WHERE material_id=?`, id).Scan(&after); err != nil {
			t.Fatal(err)
		}
		if tc.delta == 0 {
			if after != before {
				t.Fatal("no-op movement")
			}
			continue
		}
		var delta float64
		var actor int64
		if err = inv.DB().QueryRow(`SELECT quantity,user_id FROM inventory_movements WHERE material_id=? ORDER BY id DESC LIMIT 1`, id).Scan(&delta, &actor); err != nil {
			t.Fatal(err)
		}
		if after != before+1 || delta != tc.delta || actor != 1 {
			t.Fatalf("movement delta=%g actor=%d count=%d", delta, actor, after)
		}
	}
	// The balance shown at selection time is not the transaction's old balance.
	h.message(t, 900001, "/materials")
	h.click(t, 900001, "Редактировать")
	h.message(t, 900001, "1")
	if _, err = inv.AdjustStockByID(w, id, 5); err != nil {
		t.Fatal(err)
	}
	h.message(t, 900001, "20")
	var delta float64
	if err = inv.DB().QueryRow(`SELECT quantity FROM inventory_movements WHERE material_id=? ORDER BY id DESC LIMIT 1`, id).Scan(&delta); err != nil || delta != 3.5 {
		t.Fatalf("stale balance delta=%g %v", delta, err)
	}
	if !strings.Contains(h.sent[len(h.sent)-2]["text"].(string), "Было: 16.5 г") {
		t.Fatal("stale previous balance in response")
	}
}
