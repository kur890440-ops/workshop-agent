package products

import (
	"database/sql"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
	_ "time/tzdata"
	"workshop-agent/internal/auth"
)

const SummaryTimezone = "Europe/Moscow"

type DailyProduct struct {
	ID          int64
	Name, Unit  string
	Good, Scrap float64
}
type DailyProduction struct {
	Start, End   time.Time
	Products     []DailyProduct
	Unclassified int
	Denied       bool
}

// Production date is the business event date, never the row's created_at.
// Date-only legacy events belong to the workshop's local calendar day;
// timezone-less SQL timestamps follow SQLite's UTC convention.
func productionTime(value string, loc *time.Location) (time.Time, error) {
	value = strings.TrimSpace(value)
	if len(value) == 10 {
		return time.ParseInLocation("2006-01-02", value, loc)
	}
	if t, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return t, nil
	}
	return time.ParseInLocation("2006-01-02 15:04:05.999999999", value, time.UTC)
}

func (s *Service) DailyProduction(workshop int64, now time.Time) (DailyProduction, error) {
	result := DailyProduction{}
	tx, err := s.DB().Begin()
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	if err = auth.Require(tx, s.userID, workshop, auth.WorkshopRead); err != nil {
		return result, err
	}
	loc, err := time.LoadLocation(SummaryTimezone)
	if err != nil {
		return result, err
	}
	local := now.In(loc)
	result.Start = time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	result.End = result.Start.AddDate(0, 0, 1)
	for _, permission := range []auth.Permission{auth.ProductionRead, auth.ProductsRead} {
		if err = auth.Require(tx, s.userID, workshop, permission); err != nil {
			if errors.Is(err, auth.ErrDenied) {
				result.Denied = true
				return result, nil
			}
			return result, err
		}
	}
	rows, err := tx.Query(`SELECT r.product_id,p.name,r.date,r.good_quantity,r.scrap_quantity
 FROM production_records r LEFT JOIN products p ON p.id=r.product_id AND p.workshop_id=r.workshop_id
 WHERE r.workshop_id=?`, workshop)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	byID := map[int64]*DailyProduct{}
	for rows.Next() {
		var id int64
		var name, date sql.NullString
		var good, scrap float64
		if err = rows.Scan(&id, &name, &date, &good, &scrap); err != nil {
			return result, err
		}
		when, e := productionTime(date.String, loc)
		if e != nil {
			result.Unclassified++
			continue
		}
		if when.Before(result.Start) || !when.Before(result.End) {
			continue
		}
		if !name.Valid || good < 0 || scrap < 0 || math.IsNaN(good) || math.IsNaN(scrap) || math.IsInf(good, 0) || math.IsInf(scrap, 0) {
			result.Unclassified++
			continue
		}
		p := byID[id]
		if p == nil {
			p = &DailyProduct{ID: id, Name: name.String, Unit: "шт"}
			byID[id] = p
		}
		p.Good += good
		p.Scrap += scrap
	}
	if err = rows.Err(); err != nil {
		return result, err
	}
	for _, p := range byID {
		result.Products = append(result.Products, *p)
	}
	sort.Slice(result.Products, func(i, j int) bool {
		a, b := result.Products[i], result.Products[j]
		if a.Name == b.Name {
			return a.ID < b.ID
		}
		return a.Name < b.Name
	})
	return result, tx.Commit()
}

func (r DailyProduction) Text() string {
	lines := []string{fmt.Sprintf("Сегодня, %s (%s):", r.Start.Format("02.01.2006"), SummaryTimezone), "Собрано заказов: учёт пока не настроен"}
	if r.Denied {
		return strings.Join(append(lines, "Производство и брак: нет прав для просмотра."), "\n")
	}
	lines = append(lines, "", "Произведено:")
	names := map[string]int{}
	for _, p := range r.Products {
		names[p.Name]++
	}
	label := func(p DailyProduct) string {
		if names[p.Name] > 1 {
			return fmt.Sprintf("%s [товар №%d]", p.Name, p.ID)
		}
		return p.Name
	}
	count := 0
	for _, p := range r.Products {
		if p.Good > 0 {
			lines = append(lines, fmt.Sprintf("• %s — %g %s", label(p), p.Good, p.Unit))
			count++
		}
	}
	if count == 0 {
		if r.Unclassified == 0 {
			lines = append(lines, "0 шт. Сегодня продукция ещё не производилась.")
		} else {
			lines = append(lines, "По записям с достоверной датой: 0 шт. Полный итог недоступен.")
		}
	}
	lines = append(lines, "", "Брак:")
	count = 0
	for _, p := range r.Products {
		if p.Scrap > 0 {
			lines = append(lines, fmt.Sprintf("• %s — %g %s", label(p), p.Scrap, p.Unit))
			count++
		}
	}
	if count == 0 {
		lines = append(lines, "0 шт по учтённым записям.")
	}
	if r.Unclassified > 0 {
		lines = append(lines, fmt.Sprintf("Итог неполный: %d записей невозможно учесть из-за отсутствующей/некорректной даты или данных продукции. Время создания записи не используется.", r.Unclassified))
	}
	return strings.Join(lines, "\n")
}
