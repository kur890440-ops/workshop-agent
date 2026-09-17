package telegram

import (
	"testing"
)

func TestProductNameInflectionsAndWordBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, query string
		want        bool
	}{
		{"Слив боковой Advantix", "сливов", true},
		{"Слив боковой Advantix", "слива", true},
		{"Слив боковой Advantix", "СЛИВЫ ADVANTIX", true},
		{"Слив боковой Advantix", "сливов,", true},
		{"Фигурка Б", "фигурок Б", true},
		{"Набор А", "наборов А", true},
		{"Сливочный набор", "сливов", false},
		{"Перелив боковой", "сливов", false},
		{"Слив боковой Advantix", "сливов другой", false},
		{"Слив боковой Advantix", "...", false},
	} {
		if got := productNameMatches(tc.name, tc.query); got != tc.want {
			t.Errorf("%q / %q: %v", tc.name, tc.query, got)
		}
	}
}

func TestAssemblyDrainPluralUniqueAndAmbiguous(t *testing.T) {
	h, stub, w := semanticFixture(t)
	stub.fail = true
	p := h.bot.Prod.ForUser(1)
	id, err := p.CreateProduct(w, "Слив боковой Advantix", "00001", "kit", 5, 2, "")
	if err != nil {
		t.Fatal(err)
	}
	h.message(t, 900001, "собрать 5 сливов")
	h.click(t, 900001, "Выбрать")
	requireAnswer(t, h, "Слив боковой Advantix — 5 шт")
	d, err := h.bot.loadAssembly(sessionKey{900001, 1}, w)
	if err != nil || d == nil || d.Product != id || d.Quantity != 5 || d.Stage != "confirm" {
		t.Fatal(d, err)
	}
	if taskCount(t, h) != 0 {
		t.Fatal("created task before confirmation")
	}
	h.message(t, 900001, "/setup_stop")
	if _, err = p.CreateProduct(w, "Слив прямой", "00002", "kit", 0, 0, ""); err != nil {
		t.Fatal(err)
	}
	h.message(t, 900001, "собрать 5 сливов")
	requireAnswer(t, h, "неоднозначно")
	d, err = h.bot.loadAssembly(sessionKey{900001, 1}, w)
	if err != nil || d == nil || d.Product != 0 || d.Quantity != 5 {
		t.Fatal("picked arbitrary product", d, err)
	}
	h.message(t, 900001, "сливов Advantix")
	requireAnswer(t, h, "Слив боковой Advantix — 5 шт")
	if stub.calls != 0 {
		t.Fatal("local assembly used LLM", stub.calls)
	}
}
