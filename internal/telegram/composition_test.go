package telegram

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"
	"workshop-agent/internal/memory"
	"workshop-agent/internal/products"
)

func openComposition(t *testing.T, h *harness) {
	t.Helper()
	openEditor(t, h)
	h.click(t, 900001, "Состав товара")
}
func addComponent(t *testing.T, h *harness, name, qty string) {
	t.Helper()
	h.click(t, 900001, "Добавить компонент")
	s := h.bot.getSetup(900001, 1)
	n := 0
	for i, id := range s.composition.MaterialIDs {
		m, err := h.bot.Inv.ForUser(1).Material(s.workshopID, id)
		if err != nil {
			t.Fatal(err)
		}
		if m["name"] == name {
			n = i + 1
		}
	}
	if n == 0 {
		t.Fatal("material missing", name)
	}
	h.message(t, 900001, strconv.Itoa(n))
	h.message(t, 900001, qty)
}
func TestCompositionMenuSpecificPackagingAndDuplicates(t *testing.T) {
	h, s, w, p := assemblyFixture(t)
	for _, name := range []string{"Коробка 185×185×100", "Коробка 250×200×150", "Курьер-пакет 250×350"} {
		if _, err := h.bot.Inv.ForUser(1).CreateMaterial(w, name, "packaging", "pcs", 100, 0, "", 0, ""); err != nil {
			t.Fatal(err)
		}
	}
	openComposition(t, h)
	requireAnswer(t, h, "Гипс (материал) — 1 кг")
	addComponent(t, h, "Коробка 250×200×150", "1")
	snap, _ := h.bot.Prod.ForUser(1).Composition(w, p)
	if len(snap.Rows) != 1 {
		t.Fatal("saved before confirm")
	}
	h.click(t, 900001, "Сохранить")
	requireAnswer(t, h, "Коробка 250×200×150 (материал) — 1 шт")
	addComponent(t, h, "Курьер-пакет 250×350", "1")
	h.click(t, 900001, "Сохранить")
	h.click(t, 900001, "Сохранить")
	snap, _ = h.bot.Prod.ForUser(1).Composition(w, p)
	if len(snap.Rows) != 3 {
		t.Fatal("duplicate confirmation", snap)
	}
	h.message(t, 900001, "/setup_stop")
	h.click(t, 900001, "Добавить компонент")
	session := h.bot.getSetup(900001, 1)
	n := 0
	for i, id := range session.composition.MaterialIDs {
		if id == snap.Rows[1].MaterialID {
			n = i + 1
		}
	}
	h.message(t, 900001, strconv.Itoa(n))
	requireAnswer(t, h, "Состав на 1 товар")
	snap, _ = h.bot.Prod.ForUser(1).Composition(w, p)
	if len(snap.Rows) != 3 {
		t.Fatal("duplicate added")
	}
	h.click(t, 900001, "Изменить количество")
	h.message(t, 900001, "2")
	h.message(t, 900001, "2")
	h.click(t, 900001, "Сохранить")
	h.click(t, 900001, "Убрать компонент")
	h.message(t, 900001, "2")
	h.click(t, 900001, "Отмена")
	snap, _ = h.bot.Prod.ForUser(1).Composition(w, p)
	if len(snap.Rows) != 3 {
		t.Fatal("cancel delete")
	}
	h.click(t, 900001, "Убрать компонент")
	h.message(t, 900001, "2")
	h.click(t, 900001, "Сохранить")
	snap, _ = h.bot.Prod.ForUser(1).Composition(w, p)
	if len(snap.Rows) != 2 {
		t.Fatal("remove failed")
	}
	if s.calls != 0 {
		t.Fatal("LLM used")
	}
}
func TestCompositionMenuUnitsConflictAndInvalidInput(t *testing.T) {
	h, _, w, p := assemblyFixture(t)
	openComposition(t, h)
	h.click(t, 900001, "Изменить количество")
	h.message(t, 900001, "99")
	requireAnswer(t, h, "номер из показанного состава")
	h.message(t, 900001, "1")
	for _, v := range []string{"0", "-1", "+2", "NaN", "Inf", "1e999", "1 2"} {
		h.message(t, 900001, v)
		requireAnswer(t, h, "положительное количество")
	}
	h.message(t, 900001, "0,25")
	requireAnswer(t, h, "Будет: 0.25 кг")
	mid, err := h.bot.Inv.ForUser(1).GetMaterialID(w, "Гипс")
	if err != nil {
		t.Fatal(err)
	}
	if err = h.bot.Inv.ForUser(1).SetDisplayUnit(w, mid, "g"); err != nil {
		t.Fatal(err)
	}
	h.click(t, 900001, "Сохранить")
	requireAnswer(t, h, "Рабочая единица изменилась")
	requireAnswer(t, h, "Единица: г")
	h.message(t, 900001, "250")
	h.click(t, 900001, "Сохранить")
	snap, _ := h.bot.Prod.ForUser(1).Composition(w, p)
	if snap.Rows[0].Quantity != 0.25 || snap.Rows[0].Unit != "kg" {
		t.Fatal("double conversion", snap)
	}
	h.click(t, 900001, "Изменить количество")
	h.message(t, 900001, "1")
	h.message(t, 900001, "500")
	if err = h.bot.Prod.ForUser(1).SetBOMQuantity(w, p, mid, 300); err != nil {
		t.Fatal(err)
	}
	h.click(t, 900001, "Сохранить")
	requireAnswer(t, h, "Состав изменился")
	h.click(t, 900001, "Сохранить")
	snap, _ = h.bot.Prod.ForUser(1).Composition(w, p)
	if snap.Rows[0].DisplayQuantity() != "500 г" {
		t.Fatal(snap)
	}
}
func TestCompositionMenuInvalidation(t *testing.T) {
	for _, mode := range []string{"products", "session", "clear", "expire", "workshop", "revoke", "foreign"} {
		t.Run(mode, func(t *testing.T) {
			h, _, w, p := assemblyFixture(t)
			openComposition(t, h)
			h.click(t, 900001, "Убрать компонент")
			h.message(t, 900001, "1")
			switch mode {
			case "products":
				h.message(t, 900001, "/products")
			case "session":
				h.message(t, 900001, "/session new")
			case "clear":
				h.message(t, 900001, "/clear")
				h.click(t, 900001, "Очистить чат")
			case "expire":
				h.bot.getSetup(900001, 1).editExpires = time.Now().Add(-time.Minute)
			case "workshop":
				other, err := h.bot.WS.CreateOwnedWorkshop(1, "Other")
				if err != nil {
					t.Fatal(err)
				}
				for _, target := range []int64{other, w} {
					if err = h.bot.executeButton(buttonAction{Key: sessionKey{900001, 1}, Action: "switch_to", Workshop: target}); err != nil {
						t.Fatal(err)
					}
				}
			case "revoke":
				if _, err := h.bot.WS.DB().Exec("UPDATE workshop_members SET role='VIEWER' WHERE user_id=1"); err != nil {
					t.Fatal(err)
				}
			case "foreign":
				h.message(t, 900002, "/start")
				h.click(t, 900002, "Сохранить")
				snap, _ := h.bot.Prod.ForUser(1).Composition(w, p)
				if len(snap.Rows) != 1 {
					t.Fatal("foreign delete")
				}
				return
			}
			h.click(t, 900001, "Сохранить")
			snap, _ := h.bot.Prod.ForUser(1).Composition(w, p)
			if len(snap.Rows) != 1 {
				t.Fatal("stale delete")
			}
		})
	}
}
func TestCompositionReadonlyEmptyAndAssemblyPreserved(t *testing.T) {
	h, s, w, p := assemblyFixture(t)
	h.message(t, 900001, "соберем 5 заказов")
	openComposition(t, h)
	original, err := h.bot.Agent.Memory.ForUser(1).CreateWorkingMemory(memory.Scope{UserID: 1, WorkshopID: w}, "assembly", memory.TaskState{ProductID: p, Quantity: 7})
	if err != nil {
		t.Fatal(err)
	}
	h.click(t, 900001, "Убрать компонент")
	h.message(t, 900001, "1")
	h.message(t, 900001, "/setup_stop")
	d, err := h.bot.loadAssembly(sessionKey{900001, 1}, w)
	if err != nil || d == nil || d.Orders != 5 {
		t.Fatal("draft lost", d, err)
	}
	h.click(t, 900001, "Убрать компонент")
	h.message(t, 900001, "1")
	h.click(t, 900001, "Сохранить")
	requireAnswer(t, h, "Состав товара пока не задан")
	task, err := h.bot.Agent.Memory.ForUser(1).ActiveWorking(memory.Scope{UserID: 1, WorkshopID: w})
	if err != nil || task.ID != original.ID || task.State.Quantity != 7 {
		t.Fatal("task changed", task, err)
	}
	mat, err := h.bot.Inv.ForUser(1).MaterialByName(w, "Гипс")
	if err != nil || mat["current_stock"] != float64(10000) {
		t.Fatal(mat, err)
	}
	h.click(t, 900001, "Назад к товару")
	requireAnswer(t, h, "Название: Фигурка Б")
	h.message(t, 900001, "/products")
	if _, err = h.bot.WS.DB().Exec("UPDATE workshop_members SET role='VIEWER' WHERE user_id=1"); err != nil {
		t.Fatal(err)
	}
	h.message(t, 900001, "товары")
	h.click(t, 900001, "Просмотреть продукт")
	h.message(t, 900001, "2")
	h.click(t, 900001, "Состав товара")
	requireAnswer(t, h, "Состав товара пока не задан")
	raw, _ := json.Marshal(h.sent[len(h.sent)-1])
	if strings.Contains(string(raw), "Добавить компонент") {
		t.Fatal("viewer mutation button")
	}
	if s.calls != 0 {
		t.Fatal("LLM calls")
	}
}
func TestCompositionNestedMenuAndMaterialSnapshot(t *testing.T) {
	h, _, w, p := assemblyFixture(t)
	child, err := h.bot.Prod.ForUser(1).CreateProduct(w, "Я вложенный", "child", "product", 0, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if err = h.bot.Prod.ForUser(1).SetBOMItem(w, p, "product", 0, child, 2, "pcs", 5, "keep"); err != nil {
		t.Fatal(err)
	}
	openComposition(t, h)
	requireAnswer(t, h, "Я вложенный (вложенный продукт) — 2 шт")
	h.click(t, 900001, "Изменить количество")
	h.message(t, 900001, "2")
	h.message(t, 900001, "3")
	h.click(t, 900001, "Сохранить")
	snap, _ := h.bot.Prod.ForUser(1).Composition(w, p)
	if snap.Rows[1].ProductID != child || snap.Rows[1].Loss != 5 || snap.Rows[1].Notes != "keep" {
		t.Fatal(snap)
	}
	h.click(t, 900001, "Добавить компонент")
	old := append([]int64(nil), h.bot.getSetup(900001, 1).composition.MaterialIDs...)
	if _, err = h.bot.Inv.ForUser(1).CreateMaterial(w, "AAA", "raw", "pcs", 0, 0, "", 0, ""); err != nil {
		t.Fatal(err)
	}
	h.message(t, 900001, "2")
	if h.bot.getSetup(900001, 1).composition.Change.MaterialID != old[1] {
		t.Fatal("unstable material number")
	}
	h.message(t, 900001, "1")
	h.click(t, 900001, "Сохранить")
	// A foreign product/row pair cannot be edited through the service.
	snapshot, _ := h.bot.Prod.ForUser(1).Composition(w, child)
	if _, err = h.bot.Prod.ForUser(1).ApplyBOMChange(w, child, products.BOMChange{Action: "remove", RowID: snap.Rows[0].ID, Expected: snapshot.Version}); err == nil {
		t.Fatal("foreign row accepted")
	}
}

func TestCompositionTypingUnitChangeAndLongList(t *testing.T) {
	h, _, w, p := assemblyFixture(t)
	openComposition(t, h)
	h.click(t, 900001, "Изменить количество")
	h.message(t, 900001, "1")
	mid, err := h.bot.Inv.ForUser(1).GetMaterialID(w, "Гипс")
	if err != nil {
		t.Fatal(err)
	}
	if err = h.bot.Inv.ForUser(1).SetDisplayUnit(w, mid, "g"); err != nil {
		t.Fatal(err)
	}
	h.message(t, 900001, "0,25")
	requireAnswer(t, h, "Введите количество заново")
	if h.bot.getSetup(900001, 1).composition.Stage != "quantity" {
		t.Fatal("old input reused")
	}
	h.message(t, 900001, "250")
	h.click(t, 900001, "Сохранить")
	for i := 0; i < 20; i++ {
		id, err := h.bot.Inv.ForUser(1).CreateMaterial(w, strings.Repeat("Длинное название коробки ", 20), "packaging", "pcs", 0, 0, "", 0, "")
		if err != nil {
			t.Fatal(err)
		}
		if err = h.bot.Prod.ForUser(1).SetBOMItem(w, p, "material", id, 0, 1, "pcs", 0, ""); err != nil {
			t.Fatal(err)
		}
	}
	before := len(h.sent)
	h.message(t, 900001, "/setup_stop")
	if len(h.sent)-before < 2 {
		t.Fatal("composition not split")
	}
	for _, m := range h.sent[before:] {
		if text, ok := m["text"].(string); ok && len([]rune(text)) > 2048 {
			t.Fatal("Telegram length")
		}
	}
	if len(h.bot.getSetup(900001, 1).composition.Rows) != 21 {
		t.Fatal("lost snapshot rows")
	}
}
