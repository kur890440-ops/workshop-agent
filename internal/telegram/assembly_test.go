package telegram

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
	"workshop-agent/internal/auth"
	"workshop-agent/internal/memory"
)

func assemblyFixture(t *testing.T) (*harness, *semanticStub, int64, int64) {
	t.Helper()
	h, s, w := semanticFixture(t)
	s.fail = true
	p := h.bot.Prod.ForUser(1)
	if _, err := p.CreateProduct(w, "Набор А", "a", "kit", 10, 0, ""); err != nil {
		t.Fatal(err)
	}
	id, err := p.CreateProduct(w, "Фигурка Б", "b", "product", 20, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	mid, err := h.bot.Inv.ForUser(1).GetMaterialID(w, "Гипс")
	if err != nil {
		t.Fatal(err)
	}
	if err = p.SetBOMItem(w, id, "material", mid, 0, 1, "kg", 0, ""); err != nil {
		t.Fatal(err)
	}
	return h, s, w, id
}
func requireAnswer(t *testing.T, h *harness, contains string) {
	t.Helper()
	if !strings.Contains(lastAnswer(h), contains) {
		t.Fatalf("want %q, got %q", contains, lastAnswer(h))
	}
}
func taskCount(t *testing.T, h *harness) int {
	t.Helper()
	var n int
	if err := h.bot.WS.DB().QueryRow("SELECT COUNT(*) FROM working_memory").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
func TestProductsAliasesAndFormPrecedence(t *testing.T) {
	h, s, _, _ := assemblyFixture(t)
	for _, a := range []string{"/products", "товары", "продукты", "изделия", "список товаров", "покажи товары", "покажи продукты", "  ТоВаРы  "} {
		h.message(t, 900001, a)
		requireAnswer(t, h, "2. Фигурка Б — 20 шт")
	}
	if s.calls != 0 {
		t.Fatal("local aliases called LLM")
	}
	h.message(t, 900001, "/setup_add_product")
	h.message(t, 900001, "товары")
	if h.bot.getSetup(900001, 1).name != "товары" {
		t.Fatal("form input intercepted")
	}
	h.message(t, 900001, "/products")
	if h.bot.getSetup(900001, 1) != nil {
		t.Fatal("explicit command did not exit form")
	}
	if taskCount(t, h) != 0 {
		t.Fatal("list created task")
	}
	h.message(t, 900001, "2")
	if taskCount(t, h) != 0 {
		t.Fatal("bare number created task")
	}
}
func TestAssemblyOrderFlowSnapshotAndNoStockMutation(t *testing.T) {
	h, s, w, id := assemblyFixture(t)
	h.message(t, 900001, "соберём 5 заказов")
	h.message(t, 900001, "товары")
	// Reordering the database cannot reinterpret an already displayed number.
	if _, err := h.bot.WS.DB().Exec("UPDATE products SET name='ААА' WHERE id=?", id); err != nil {
		t.Fatal(err)
	}
	h.message(t, 900001, "второй")
	requireAnswer(t, h, "Сколько штук входит в один заказ")
	h.message(t, 900001, "3")
	requireAnswer(t, h, "5 заказов × 3 шт = 15 шт")
	requireAnswer(t, h, "нужно 15 кг; есть 10 кг; дефицит 5 кг")
	if taskCount(t, h) != 0 {
		t.Fatal("task before confirmation")
	}
	h.click(t, 900001, "Создать задачу")
	sc := memory.Scope{UserID: 1, WorkshopID: w}
	task, err := h.bot.Agent.Memory.ForUser(1).ActiveWorking(sc)
	if err != nil || task == nil || task.State.ProductID != id || task.State.Quantity != 15 {
		t.Fatalf("wrong task %+v %v", task, err)
	}
	h.click(t, 900001, "Создать задачу")
	if taskCount(t, h) != 1 {
		t.Fatal("duplicate task")
	}
	item, err := h.bot.Inv.ForUser(1).MaterialByName(w, "Гипс")
	if err != nil || item["current_stock"] != float64(10000) {
		t.Fatal("stock changed", item, err)
	}
	var stock float64
	var moves int
	if err = h.bot.WS.DB().QueryRow("SELECT current_stock FROM products WHERE id=?", id).Scan(&stock); err != nil {
		t.Fatal(err)
	}
	if err = h.bot.WS.DB().QueryRow("SELECT COUNT(*) FROM inventory_movements").Scan(&moves); err != nil {
		t.Fatal(err)
	}
	if stock != 20 || moves != 0 {
		t.Fatal("calculation changed stock", stock, moves)
	}
	if s.calls != 0 {
		t.Fatal("assembly used LLM", s.calls)
	}
}
func TestAssemblyQuantityAndTypedLists(t *testing.T) {
	h, _, w, _ := assemblyFixture(t)
	h.message(t, 900001, "соберем 5 заказов")
	h.message(t, 900001, "материалы")
	h.message(t, 900001, "2")
	requireAnswer(t, h, "актуальном списке продуктов")
	h.message(t, 900001, "товары")
	h.message(t, 900001, "99")
	requireAnswer(t, h, "Нет такого номера")
	h.message(t, 900001, "2")
	for _, v := range []string{"0", "-1", "+3", "1.5", "2,5", "NaN", "Inf", "100000000000000000000", "1000000000"} {
		h.message(t, 900001, v)
		d, e := h.bot.loadAssembly(sessionKey{900001, 1}, w)
		if e != nil || d.Stage != "quantity" {
			t.Fatal(v, d, e)
		}
	}
	h.message(t, 900001, "3")
	requireAnswer(t, h, "15 шт")
	h.message(t, 900001, "/setup_stop")
	h.click(t, 900001, "Создать задачу")
	if taskCount(t, h) != 0 {
		t.Fatal("cancelled draft saved")
	}
	h.message(t, 900001, "соберём 5 фигурок Б")
	requireAnswer(t, h, "Фигурка Б — 5 шт")
	if strings.Contains(lastAnswer(h), "один заказ") {
		t.Fatal("total treated as orders")
	}
}
func TestAssemblyInvalidation(t *testing.T) {
	for _, mode := range []string{"expire", "session", "workshop", "clear", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			h, _, w, _ := assemblyFixture(t)
			h.message(t, 900001, "соберём 5 фигурок Б")
			switch mode {
			case "expire":
				_, err := h.bot.WS.DB().Exec("UPDATE pending_actions SET expires_at=?", time.Now().Add(-time.Hour).UTC().Format(time.RFC3339))
				if err != nil {
					t.Fatal(err)
				}
			case "session":
				h.message(t, 900001, "/session new")
			case "workshop":
				other, err := h.bot.WS.CreateOwnedWorkshop(1, "Other")
				if err != nil {
					t.Fatal(err)
				}
				if err = h.bot.executeButton(buttonAction{Key: sessionKey{900001, 1}, Action: "switch_to", Workshop: other}); err != nil {
					t.Fatal(err)
				}
				if err = h.bot.executeButton(buttonAction{Key: sessionKey{900001, 1}, Action: "switch_to", Workshop: w}); err != nil {
					t.Fatal(err)
				}
			case "clear":
				h.message(t, 900001, "/clear")
				h.click(t, 900001, "Очистить чат")
			case "cancel":
				h.click(t, 900001, "Отмена")
			}
			h.click(t, 900001, "Создать задачу")
			if taskCount(t, h) != 0 {
				t.Fatal("invalidated calculation saved")
			}
		})
	}
}
func TestAssemblyPermissionsExistingTaskAndAtomicity(t *testing.T) {
	for _, mode := range []string{"revoke", "existing", "rollback", "foreign"} {
		t.Run(mode, func(t *testing.T) {
			h, _, w, _ := assemblyFixture(t)
			h.message(t, 900001, "соберём 5 фигурок Б")
			switch mode {
			case "revoke":
				if _, err := h.bot.WS.DB().Exec("UPDATE workshop_members SET role='VIEWER' WHERE user_id=1"); err != nil {
					t.Fatal(err)
				}
			case "existing":
				if _, err := h.bot.Agent.Memory.ForUser(1).CreateWorkingMemory(memory.Scope{UserID: 1, WorkshopID: w}, "assembly", memory.TaskState{Quantity: 7}); err != nil {
					t.Fatal(err)
				}
			case "rollback":
				if _, err := h.bot.WS.DB().Exec("CREATE TRIGGER fail_task_audit BEFORE INSERT ON audit_logs WHEN NEW.event_type='TASK_CREATED' BEGIN SELECT RAISE(ABORT,'test'); END"); err != nil {
					t.Fatal(err)
				}
			case "foreign":
				h.message(t, 900002, "/start")
				h.click(t, 900002, "Создать задачу")
				if taskCount(t, h) != 0 {
					t.Fatal("foreign callback saved")
				}
				return
			}
			h.click(t, 900001, "Создать задачу")
			want := 0
			if mode == "existing" {
				want = 1
				requireAnswer(t, h, "/task cancel")
			}
			if taskCount(t, h) != want {
				t.Fatal(mode, "unexpected task")
			}
			d, err := h.bot.loadAssembly(sessionKey{900001, 1}, w)
			if err != nil || d == nil {
				t.Fatal("failure consumed draft", err)
			}
		})
	}
}
func TestProductsEmptyAndMissingAmbiguousBOM(t *testing.T) {
	h, s, w := semanticFixture(t)
	s.fail = true
	h.message(t, 900001, "товары")
	requireAnswer(t, h, "Продуктов пока нет")
	h.click(t, 900001, "Добавить продукт")
	if h.bot.getSetup(900001, 1) == nil {
		t.Fatal("missing form")
	}
	h.message(t, 900001, "/setup_stop")
	for _, name := range []string{"Фигурка А", "Фигурка Б"} {
		if _, err := h.bot.Prod.ForUser(1).CreateProduct(w, name, name, "product", 0, 0, ""); err != nil {
			t.Fatal(err)
		}
	}
	h.message(t, 900001, "соберем 5 фигурок")
	requireAnswer(t, h, "неоднозначно")
	h.message(t, 900001, "товары")
	h.message(t, 900001, "1")
	requireAnswer(t, h, "BOM отсутствует")
	h.message(t, 900001, "/setup_stop")
	if _, err := h.bot.WS.DB().Exec("UPDATE workshop_members SET role=? WHERE user_id=1", auth.Viewer); err != nil {
		t.Fatal(err)
	}
	before := len(h.sent)
	h.message(t, 900001, "товары")
	for _, msg := range h.sent[before:] {
		raw, _ := json.Marshal(msg["reply_markup"])
		if strings.Contains(string(raw), "Добавить продукт") {
			t.Fatal("viewer has add button")
		}
	}
	h.message(t, 900001, "соберем 5 заказов")
	if taskCount(t, h) != 0 {
		t.Fatal("viewer created calculation")
	}
}

func TestProductsLongListAndScope(t *testing.T) {
	h, _, w, _ := assemblyFixture(t)
	for i := 0; i < 30; i++ {
		if _, err := h.bot.Prod.ForUser(1).CreateProduct(w, strings.Repeat("Длинное название ", 20), "", "product", 0, 0, ""); err != nil {
			t.Fatal(err)
		}
	}
	before := len(h.sent)
	h.message(t, 900001, "товары")
	if len(h.sent)-before < 2 {
		t.Fatal("list not split")
	}
	for _, msg := range h.sent[before:] {
		if text, ok := msg["text"].(string); ok && len([]rune(text)) > 4096 {
			t.Fatal("Telegram limit")
		}
	}
	snap := h.bot.currentList(sessionKey{900001, 1}, w)
	if len(snap.IDs) != 32 {
		t.Fatal("lost IDs", len(snap.IDs))
	}
	h.message(t, 900001, "соберем 5 заказов")
	h.message(t, 900002, "/start")
	var otherUser int64
	if err := h.bot.WS.DB().QueryRow("SELECT id FROM users WHERE telegram_user_id=900002").Scan(&otherUser); err != nil {
		t.Fatal(err)
	}
	if _, err := h.bot.WS.CreateOwnedWorkshop(otherUser, "B"); err != nil {
		t.Fatal(err)
	}
	h.message(t, 900002, "товары")
	requireAnswer(t, h, "Продуктов пока нет")
	if strings.Contains(lastAnswer(h), "текущей сборки") {
		t.Fatal("foreign draft leaked")
	}
}
