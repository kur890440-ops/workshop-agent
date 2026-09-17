package telegram

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
	"workshop-agent/internal/memory"
	"workshop-agent/internal/products"
)

func openEditor(t *testing.T, h *harness) {
	t.Helper()
	h.message(t, 900001, "товары")
	h.click(t, 900001, "Редактировать продукт")
	h.message(t, 900001, "2")
}
func productValue(t *testing.T, h *harness, w, id int64, field string) string {
	t.Helper()
	p, err := h.bot.Prod.ForUser(1).Product(w, id)
	if err != nil {
		t.Fatal(err)
	}
	return p.Field(field)
}
func TestProductEditorFieldsAndLocalFlow(t *testing.T) {
	h, s, w, id := assemblyFixture(t)
	s.calls = 0
	openEditor(t, h)
	requireAnswer(t, h, "Тип: Готовое изделие")
	for _, tc := range []struct{ button, input, field, want string }{
		{"Название", "Товары", "name", "Товары"}, {"Артикул (SKU)", "00001", "sku", "00001"}, {"Артикул (SKU)", "-", "sku", ""},
		{"Минимальный остаток", "2,5", "minimum_stock", "2.5"}, {"Минимальный остаток", "0", "minimum_stock", "0"},
		{"Остаток", "10", "current_stock", "10"}, {"Остаток", "+3", "current_stock", "13"}, {"Остаток", "-2,5", "current_stock", "10.5"}, {"Остаток", "0", "current_stock", "0"},
	} {
		before := productValue(t, h, w, id, tc.field)
		h.click(t, 900001, tc.button)
		h.message(t, 900001, tc.input)
		if got := productValue(t, h, w, id, tc.field); got != before {
			t.Fatal("write before confirmation")
		}
		h.click(t, 900001, "Сохранить")
		if got := productValue(t, h, w, id, tc.field); got != tc.want {
			t.Fatal(tc, got)
		}
	}
	h.click(t, 900001, "Тип продукта")
	h.click(t, 900001, "Набор")
	h.click(t, 900001, "Сохранить")
	requireAnswer(t, h, "Тип: Набор")
	if s.calls != 0 {
		t.Fatal("menu used LLM", s.calls)
	}
	var count int
	h.bot.WS.DB().QueryRow("SELECT COUNT(*) FROM bom_items WHERE product_id=?", id).Scan(&count)
	if count != 1 {
		t.Fatal("BOM changed")
	}
	h.click(t, 900001, "Назад к товарам")
	requireAnswer(t, h, "Продукты:")
}
func TestProductEditorConcurrentChangesAndRepeatedClick(t *testing.T) {
	h, _, w, id := assemblyFixture(t)
	openEditor(t, h)
	h.click(t, 900001, "Остаток")
	h.message(t, 900001, "+3")
	if _, err := h.bot.Prod.ForUser(1).ApplyEdit(w, id, products.Edit{Field: "current_stock", Value: "30"}); err != nil {
		t.Fatal(err)
	}
	h.click(t, 900001, "Сохранить")
	requireAnswer(t, h, "Данные изменились")
	requireAnswer(t, h, "Будет: 33 шт")
	if productValue(t, h, w, id, "current_stock") != "30" {
		t.Fatal("silent overwrite")
	}
	h.click(t, 900001, "Сохранить")
	if productValue(t, h, w, id, "current_stock") != "33" {
		t.Fatal("fresh delta")
	}
	h.click(t, 900001, "Сохранить")
	if productValue(t, h, w, id, "current_stock") != "33" {
		t.Fatal("duplicate delta")
	}
	// Text fields use the same optimistic comparison.
	h.message(t, 900001, "/products")
	h.click(t, 900001, "Редактировать продукт")
	h.message(t, 900001, "2")
	h.click(t, 900001, "Название")
	h.message(t, 900001, "My name")
	if _, err := h.bot.Prod.ForUser(1).ApplyEdit(w, id, products.Edit{Field: "name", Value: "Other name"}); err != nil {
		t.Fatal(err)
	}
	h.click(t, 900001, "Сохранить")
	requireAnswer(t, h, "Было: Other name")
	if productValue(t, h, w, id, "name") != "Other name" {
		t.Fatal("lost concurrent name")
	}
	h.click(t, 900001, "Отмена")
	if productValue(t, h, w, id, "name") != "Other name" {
		t.Fatal("cancel wrote")
	}
}
func TestProductEditorInvalidationAndPermissions(t *testing.T) {
	for _, mode := range []string{"stop", "products", "session", "clear", "expire", "workshop", "revoke", "foreign"} {
		t.Run(mode, func(t *testing.T) {
			h, _, w, id := assemblyFixture(t)
			openEditor(t, h)
			h.click(t, 900001, "Остаток")
			h.message(t, 900001, "+3")
			switch mode {
			case "stop":
				h.message(t, 900001, "/setup_stop")
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
				other, err := h.bot.WS.CreateOwnedWorkshop(1, "B")
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
				if productValue(t, h, w, id, "current_stock") != "20" {
					t.Fatal("foreign callback")
				}
				return
			}
			h.click(t, 900001, "Сохранить")
			if productValue(t, h, w, id, "current_stock") != "20" {
				t.Fatal("stale confirmation saved")
			}
		})
	}
}
func TestProductEditorSnapshotAndAssembly(t *testing.T) {
	h, s, w, id := assemblyFixture(t)
	h.message(t, 900001, "соберем 5 заказов")
	h.message(t, 900001, "товары")
	h.click(t, 900001, "Редактировать продукт")
	if _, err := h.bot.Prod.ForUser(1).CreateProduct(w, "AAA", "a", "product", 0, 0, ""); err != nil {
		t.Fatal(err)
	}
	h.message(t, 900001, "99")
	requireAnswer(t, h, "номер продукта")
	h.message(t, 900001, "2")
	requireAnswer(t, h, "Фигурка Б")
	h.click(t, 900001, "Название")
	h.message(t, 900001, "Фигурка В")
	h.click(t, 900001, "Сохранить")
	if productValue(t, h, w, id, "name") != "Фигурка В" {
		t.Fatal("unstable product ID")
	}
	d, err := h.bot.loadAssembly(sessionKey{900001, 1}, w)
	if err != nil || d == nil || d.Orders != 5 || d.Product != 0 {
		t.Fatal("draft damaged", d, err)
	}
	h.message(t, 900001, "/setup_stop")
	h.message(t, 900001, "3")
	h.message(t, 900001, "3")
	requireAnswer(t, h, "15 шт")
	h.click(t, 900001, "Создать задачу")
	task, err := h.bot.Agent.Memory.ForUser(1).ActiveWorking(memory.Scope{UserID: 1, WorkshopID: w})
	if err != nil || task == nil || task.State.ProductID != id {
		t.Fatal("assembly failed", task, err)
	}
	if s.calls != 0 {
		t.Fatal("LLM called")
	}
}
func TestProductEditorInvalidNumbersAndViewerMenu(t *testing.T) {
	h, _, w, id := assemblyFixture(t)
	openEditor(t, h)
	h.click(t, 900001, "Остаток")
	for _, v := range []string{"NaN", "Inf", "+0", "-0", "-21", "1e999", "1 2", "1000000000001"} {
		h.message(t, 900001, v)
		requireAnswer(t, h, "Некорректное значение")
		if productValue(t, h, w, id, "current_stock") != "20" {
			t.Fatal(v)
		}
	}
	h.message(t, 900001, "20")
	h.click(t, 900001, "Сохранить")
	var n int
	h.bot.WS.DB().QueryRow("SELECT COUNT(*) FROM product_movements").Scan(&n)
	if n != 0 {
		t.Fatal("unchanged movement")
	}
	h.message(t, 900001, "/products")
	if _, err := h.bot.WS.DB().Exec("UPDATE workshop_members SET role='VIEWER' WHERE user_id=1"); err != nil {
		t.Fatal(err)
	}
	before := len(h.sent)
	h.message(t, 900001, "товары")
	for _, msg := range h.sent[before:] {
		raw, _ := json.Marshal(msg["reply_markup"])
		if strings.Contains(string(raw), "Редактировать продукт") || strings.Contains(string(raw), "Добавить продукт") {
			t.Fatal("viewer edit button")
		}
	}
	h.click(t, 900001, "Редактировать продукт")
	if h.bot.getSetup(900001, 1) != nil {
		t.Fatal("viewer editor opened")
	}
}

func TestProductCreationReturnsMenu(t *testing.T) {
	h, s, _ := semanticFixture(t)
	h.message(t, 900001, "товары")
	h.click(t, 900001, "Добавить продукт")
	for _, v := range []string{"Новое изделие", "00001", "2", "5,5", "2", "да"} {
		h.message(t, 900001, v)
	}
	requireAnswer(t, h, "Новое изделие — 5.5 шт")
	h.click(t, 900001, "Редактировать продукт")
	h.message(t, 900001, "1")
	requireAnswer(t, h, "SKU: 00001")
	requireAnswer(t, h, "Тип: Набор")
	if s.calls != 0 {
		t.Fatal("LLM used")
	}
}

func TestProductEditorPreservesTaskAndDomainScope(t *testing.T) {
	h, _, w, id := assemblyFixture(t)
	sc := memory.Scope{UserID: 1, WorkshopID: w}
	original, err := h.bot.Agent.Memory.ForUser(1).CreateWorkingMemory(sc, "assembly", memory.TaskState{ProductID: id, Quantity: 7})
	if err != nil {
		t.Fatal(err)
	}
	openEditor(t, h)
	h.click(t, 900001, "Остаток")
	h.message(t, 900001, "+3")
	h.click(t, 900001, "Сохранить")
	task, err := h.bot.Agent.Memory.ForUser(1).ActiveWorking(sc)
	if err != nil || task.ID != original.ID || task.State.Quantity != 7 {
		t.Fatal("task changed", task, err)
	}
	mat, err := h.bot.Inv.ForUser(1).MaterialByName(w, "Гипс")
	if err != nil || mat["current_stock"] != float64(10000) {
		t.Fatal("materials changed", mat, err)
	}
	var records int
	h.bot.WS.DB().QueryRow("SELECT COUNT(*) FROM production_records").Scan(&records)
	if records != 0 {
		t.Fatal("fake production")
	}
	other, err := h.bot.WS.CreateOwnedWorkshop(1, "Other")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.bot.Prod.ForUser(1).ApplyEdit(other, id, products.Edit{Field: "current_stock", Value: "0"}); err == nil {
		t.Fatal("cross-workshop ID accepted")
	}
}
