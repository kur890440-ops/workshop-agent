package products

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
	"workshop-agent/internal/testkit"
)

func TestDailyProductionFactsDatesIsolation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daily.db")
	u, w := testkit.Owner(t, path)
	s := NewBOMService(path).ForUser(u)
	defer s.Close()
	p, err := s.CreateProduct(w, "Одинаковое имя", "A", "product", 999, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	q, err := s.CreateProduct(w, "Одинаковое имя", "B", "kit", 888, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	empty, err := s.DailyProduction(w, now)
	if err != nil || !strings.Contains(empty.Text(), "Сегодня продукция ещё не производилась") {
		t.Fatal(empty, err)
	}
	insert := func(workshop, product int64, date any, good, scrap float64) {
		t.Helper()
		_, e := s.DB().Exec(`INSERT INTO production_records(workshop_id,product_id,date,attempted_quantity,good_quantity,scrap_quantity,created_at) VALUES(?,?,?,?,?,?,?)`, workshop, product, date, good+scrap, good, scrap, "2000-01-01 00:00:00")
		if e != nil {
			t.Fatal(e)
		}
	}
	insert(w, p, "2026-09-16T20:59:59Z", 100, 0) // yesterday in Moscow
	insert(w, p, "2026-09-16T21:00:00Z", 2, 1)   // inclusive midnight
	insert(w, p, "2026-09-17 20:59:59", 3, 2)    // SQLite UTC
	insert(w, p, "2026-09-17T21:00:00Z", 100, 0) // exclusive next midnight
	insert(w, q, "2026-09-17", 4, 0)             // business date, local day
	insert(w, q, "2026-09-17T23:59:59+03:00", 1, 1)
	insert(w+100, p, "2026-09-17", 999, 999)
	// Stock movements and unfinished tasks are not facts of completed production.
	_, err = s.DB().Exec(`INSERT INTO product_movements(workshop_id,product_id,quantity,movement_type,date,reference_type,reference_id) VALUES(?,?,100,'adjustment','2026-09-17','production',1)`, w, p)
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.DailyProduction(w, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Products) != 2 || r.Products[0].Good != 5 || r.Products[0].Scrap != 3 || r.Products[1].Good != 5 || r.Products[1].Scrap != 1 {
		t.Fatal(r)
	}
	if r.Start.UTC().Format(time.RFC3339) != "2026-09-16T21:00:00Z" || r.End.Sub(r.Start) != 24*time.Hour {
		t.Fatal(r.Start, r.End)
	}
	if !strings.Contains(r.Text(), "учёт пока не настроен") || !strings.Contains(r.Text(), "товар №") {
		t.Fatal(r.Text())
	}
	again, err := s.DailyProduction(w, now)
	if err != nil || again.Text() != r.Text() {
		t.Fatal("read changed totals", err)
	}
	if _, err = s.ForUser(u+100).DailyProduction(w, now); err == nil {
		t.Fatal("foreign user read")
	}
	if _, err = s.DailyProduction(w+100, now); err == nil {
		t.Fatal("foreign workshop read")
	}
	insert(w, p, nil, 50, 0)
	insert(w, p, "invalid", 20, 0)
	r, err = s.DailyProduction(w, now)
	if err != nil || r.Unclassified != 2 || r.Products[0].Good != 5 || !strings.Contains(r.Text(), "Итог неполный") {
		t.Fatal(r, err)
	}
	// No creation-time fallback even if it is today.
	_, err = s.DB().Exec(`UPDATE production_records SET created_at='2026-09-17 10:00:00' WHERE date IS NULL`)
	if err != nil {
		t.Fatal(err)
	}
	r, err = s.DailyProduction(w, now.AddDate(0, 0, 5))
	if err != nil || strings.Contains(r.Text(), "Сегодня продукция ещё не производилась") {
		t.Fatal(r, err)
	}
}

func TestProductionTimeValidation(t *testing.T) {
	loc, err := time.LoadLocation(SummaryTimezone)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"", "2026-02-30", "today", "17.09.2026"} {
		if _, e := productionTime(value, loc); e == nil {
			t.Fatalf("accepted %q", value)
		}
	}
	denied := (DailyProduction{Start: time.Now(), Denied: true}).Text()
	if !strings.Contains(denied, "нет прав") || strings.Contains(denied, "0 шт") {
		t.Fatal(denied)
	}
}
