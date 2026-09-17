package agent

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"workshop-agent/internal/auth"
	"workshop-agent/internal/inventory"
	"workshop-agent/internal/llm"
	"workshop-agent/internal/products"
	"workshop-agent/internal/workshops"
)

func day11(t *testing.T) (*WorkshopAgent, int64, int64) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "day11.db")
	ws := workshops.NewService(p)
	inv := inventory.NewService(p)
	prod := products.NewBOMService(p)
	t.Cleanup(func() { ws.Close(); inv.Close(); prod.Close() })
	user, err := ws.UpsertUser(800001, "", "Owner", "")
	if err != nil {
		t.Fatal(err)
	}
	w, err := ws.CreateOwnedWorkshop(user, "A")
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []struct {
		name, unit string
		qty        float64
	}{{"PETG White", "kg", 8.4}, {"PETG Black", "kg", 4.2}, {"Кисть", "pcs", 100}} {
		if _, err := inv.ForUser(user).CreateMaterial(w, v.name, "raw", v.unit, v.qty, 0, "", 0, ""); err != nil {
			t.Fatal(err)
		}
	}
	product, err := prod.ForUser(user).CreateProduct(w, "Набор", "", "kit", 0, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := prod.ForUser(user).SetBOMItem(w, product, "material", 3, 0, 2, "pcs", 0, ""); err != nil {
		t.Fatal(err)
	}
	return NewWorkshopAgent(&llm.MockClient{}, ws, inv, prod), user, w
}
func turn(t *testing.T, a *WorkshopAgent, u, w int64, text string) string {
	t.Helper()
	answer, _, err := a.HandleMessageForWorkshop(context.Background(), w, u, u, text)
	if err != nil {
		t.Fatal(err)
	}
	return answer
}
func TestDay11ReferenceAndNewSession(t *testing.T) {
	a, u, w := day11(t)
	answer := turn(t, a, u, w, "Какой остаток белого PETG?")
	if !strings.Contains(answer, "8.4") {
		t.Fatal(answer)
	}
	answer = turn(t, a, u, w, "А черного?")
	if !strings.Contains(answer, "4.2") {
		t.Fatal(answer)
	}
	turn(t, a, u, w, "/session new")
	answer = turn(t, a, u, w, "А черного?")
	if !strings.Contains(answer, "Уточните") {
		t.Fatal(answer)
	}
}
func TestDay11QuantityAndTemporaryOverride(t *testing.T) {
	a, u, w := day11(t)
	turn(t, a, u, w, "Соберем 20 наборов.")
	answer := turn(t, a, u, w, "Нет, сделай 25.")
	if !strings.Contains(answer, "25 шт") || !strings.Contains(answer, "50 шт по BOM") {
		t.Fatal(answer)
	}
	turn(t, a, u, w, "Для этой партии используй Box B.")
	answer = turn(t, a, u, w, "Что в плане партии?")
	if !strings.Contains(answer, "Box B") {
		t.Fatal(answer)
	}
	var n int
	if err := a.WS.DB().QueryRow(`SELECT COUNT(*) FROM long_term_memory`).Scan(&n); err != nil || n != 0 {
		t.Fatal("temporary override persisted", err)
	}
	turn(t, a, u, w, "/task cancel")
	answer = turn(t, a, u, w, "Соберем 10 наборов.")
	if strings.Contains(answer, "25 шт") || strings.Contains(answer, "Box B") {
		t.Fatal("completed state leaked", answer)
	}
}
func TestDay11PreferenceAcrossSessionsAndRestart(t *testing.T) {
	a, u, w := day11(t)
	turn(t, a, u, w, "Запомни, что мне удобнее, когда итог идет первым.")
	var n int
	a.WS.DB().QueryRow(`SELECT COUNT(*) FROM user_preferences WHERE settings_json!='{}'`).Scan(&n)
	if n != 0 {
		t.Fatal("saved without confirmation")
	}
	turn(t, a, u, w, "/memory confirm")
	turn(t, a, u, w, "/session new")
	b := NewWorkshopAgent(a.LLM, a.WS, a.Inv, a.Prod)
	answer := turn(t, b, u, w, "Подготовь отчет по складу")
	if !strings.HasPrefix(answer, "Краткий итог:") {
		t.Fatal(answer)
	}
}
func TestDay11BOMSourceOfTruthAndPermission(t *testing.T) {
	a, u, w := day11(t)
	turn(t, a, u, w, "Соберем 20 наборов.")
	turn(t, a, u, w, "Теперь всегда клади в этот комплект 3 кисти.")
	turn(t, a, u, w, "/memory confirm")
	var quantity float64
	if err := a.WS.DB().QueryRow(`SELECT quantity FROM bom_items WHERE workshop_id=? AND product_id=1 AND material_id=3`, w).Scan(&quantity); err != nil || quantity != 3 {
		t.Fatal(quantity, err)
	}
	var n int
	a.WS.DB().QueryRow(`SELECT COUNT(*) FROM long_term_memory`).Scan(&n)
	if n != 0 {
		t.Fatal("BOM duplicated in memory")
	}
	employee, err := a.WS.UpsertUser(800002, "", "Employee", "")
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := a.WS.CreateInvite(u, w, auth.Employee, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.WS.AcceptInvite(employee, token); err != nil {
		t.Fatal(err)
	}
	_, _, err = a.HandleMessageForWorkshop(context.Background(), w, employee, employee, "Теперь всегда клади в Набор 4 кисти.")
	if err == nil {
		t.Fatal("employee changed BOM")
	}
}
func TestDay11DomainWinsOverStaleImportedMemory(t *testing.T) {
	a, u, w := day11(t)
	_, err := a.WS.DB().Exec(`INSERT INTO long_term_memory(memory_type,category,scope_type,workshop_id,key,value_json,source_type,created_by_user_id) VALUES('WORKSHOP_KNOWLEDGE','history','workshop',?,'petg','"раньше было около 10 кг"','legacy_test_fixture',?)`, w, u)
	if err != nil {
		t.Fatal(err)
	}
	answer := turn(t, a, u, w, "Какой остаток белого PETG?")
	if !strings.Contains(answer, "8.4") || strings.Contains(answer, "10 кг") {
		t.Fatal(answer)
	}
}

func TestMemoryOutageDoesNotFabricateStock(t *testing.T) {
	a, u, w := day11(t)
	if _, err := a.WS.DB().Exec(`DROP TABLE conversation_messages`); err != nil {
		t.Fatal(err)
	}
	answer, _, err := a.HandleMessageForWorkshop(context.Background(), w, u, u, "Какой остаток белого PETG?")
	if err == nil || answer != "" {
		t.Fatal("memory outage fabricated a response")
	}
	stock, err := a.Inv.ForUser(u).GetMaterialStock(w, "PETG White")
	if err != nil || stock != 8400 {
		t.Fatal("domain stock affected by memory outage", stock, err)
	}
}
