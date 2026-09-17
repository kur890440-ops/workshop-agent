package telegram

import (
	"strings"
	"testing"
	"unicode/utf16"
)

func TestDailySummaryLocalSemanticAndMenu(t *testing.T) {
	h, stub, _ := semanticFixture(t)
	stub.raw = `{"action":"get_daily_summary"}`
	for _, text := range []string{"/summary", "сводка", "  СВОДКА ЗА СЕГОДНЯ  ", "сколько сегодня заказов собрано", "сколько сегодня сделали", "что сегодня произвели"} {
		stub.context = ""
		h.message(t, 900001, text)
		requireAnswer(t, h, "Собрано заказов: учёт пока не настроен")
		requireAnswer(t, h, "Произведено:")
		requireAnswer(t, h, "Гипс")
		if stub.context != "" {
			t.Fatal("local summary called LLM")
		}
	}
	h.message(t, 900001, "каковы итоги сегодняшнего выпуска?")
	requireAnswer(t, h, "Собрано заказов: учёт пока не настроен")
	if stub.context == "" {
		t.Fatal("semantic route not exercised")
	}
	h.message(t, 900001, "/workshop")
	h.click(t, 900001, "📊 Сводка за сегодня")
	requireAnswer(t, h, "Europe/Moscow")
	h.message(t, 900002, "/summary")
	if strings.Contains(lastAnswer(h), "Гипс") {
		t.Fatal("cross-user leak")
	}
}

func TestLongSummaryDelivery(t *testing.T) {
	h := botFixture(t)
	text := "Сегодня:\n" + strings.Repeat("• Продукт 📦 — 12 шт\n", 1000)
	start := len(h.sent)
	if err := h.bot.sendMessage(900001, text); err != nil {
		t.Fatal(err)
	}
	var joined strings.Builder
	for _, message := range h.sent[start:] {
		part, ok := message["text"].(string)
		if !ok {
			continue
		}
		if len(utf16.Encode([]rune(part))) > 4096 {
			t.Fatal("Telegram limit exceeded")
		}
		joined.WriteString(part)
	}
	if joined.String() != text || len(h.sent)-start < 2 {
		t.Fatal("lost or unsplit summary")
	}
}
