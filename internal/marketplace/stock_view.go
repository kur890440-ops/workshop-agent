package marketplace

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	_ "time/tzdata"
	"workshop-agent/internal/auth"
	wb "workshop-agent/internal/marketplace/wildberries"
)

const StockPageSize = 15

// StockDisplayItem is a local read model, not a new source of truth.
type StockDisplayItem struct {
	ProductName, VendorCode, Variant, WarehouseName, CapturedAt, Source string
	Quantity                                                            int64
}
type StockDisplayPage struct {
	Items         []StockDisplayItem
	Total, Offset int
}

func (s *Service) StockPage(sc Scope, kind string, offset int) (StockDisplayPage, error) {
	p := StockDisplayPage{Offset: offset}
	if (kind != "seller_stocks" && kind != "wb_stocks") || offset < 0 || offset > 10000 {
		return p, ErrInput
	}
	if e := authorize(s.db, sc, auth.MarketplaceRead); e != nil {
		return p, e
	}
	c, e := connection(s.db, sc, false)
	if e != nil {
		return p, e
	}
	if e = s.db.QueryRow(`SELECT COUNT(*) FROM marketplace_stocks WHERE workshop_id=? AND connection_id=? AND kind=?`, sc.WorkshopID, c.ID, kind).Scan(&p.Total); e != nil {
		return p, ErrStorage
	}
	rows, e := s.db.Query(`SELECT s.nm_id,s.warehouse_id,s.quantity,s.fetched_at,s.warehouse_name,COALESCE(c.title,''),COALESCE(c.vendor_code,''),COALESCE(v.size,'') FROM marketplace_stocks s LEFT JOIN marketplace_cards c ON c.connection_id=s.connection_id AND c.workshop_id=s.workshop_id AND c.nm_id=s.nm_id LEFT JOIN marketplace_variants v ON v.connection_id=s.connection_id AND v.workshop_id=s.workshop_id AND v.chrt_id=s.chrt_id AND v.nm_id=s.nm_id WHERE s.workshop_id=? AND s.connection_id=? AND s.kind=? ORDER BY COALESCE(NULLIF(c.title,''),NULLIF(c.vendor_code,''),CAST(s.nm_id AS TEXT)) COLLATE NOCASE,COALESCE(v.size,''),s.warehouse_name,s.warehouse_id,s.nm_id,s.chrt_id LIMIT ? OFFSET ?`, sc.WorkshopID, c.ID, kind, StockPageSize, offset)
	if e != nil {
		return p, ErrStorage
	}
	defer rows.Close()
	for rows.Next() {
		var x StockDisplayItem
		var nm, wh int64
		if rows.Scan(&nm, &wh, &x.Quantity, &x.CapturedAt, &x.WarehouseName, &x.ProductName, &x.VendorCode, &x.Variant) != nil {
			return p, ErrStorage
		}
		x.Source = kind
		if strings.TrimSpace(x.ProductName) == "" {
			x.ProductName = x.VendorCode
		}
		if strings.TrimSpace(x.ProductName) == "" {
			x.ProductName = fmt.Sprintf("Товар WB #%d", nm)
		}
		if strings.TrimSpace(x.WarehouseName) == "" {
			title := "Склад продавца"
			if kind == "wb_stocks" {
				title = "Склад WB"
			}
			x.WarehouseName = fmt.Sprintf("%s #%d", title, wh)
		}
		p.Items = append(p.Items, x)
	}
	if rows.Err() != nil {
		return p, ErrStorage
	}
	return p, nil
}
func stockStatus(s string) string {
	switch strings.ToUpper(s) {
	case "SUCCESS", "SUCCEEDED":
		return "Данные получены"
	case "PARTIAL":
		return "Получены частично"
	case "PERMISSION_DENIED":
		return "Нет доступа"
	case "RATE_LIMITED":
		return "Временно ограничено WB"
	case "RUNNING":
		return "Загрузка выполняется"
	case "FAILED":
		return "Ошибка загрузки"
	case "CANCELLED":
		return "Загрузка прервана"
	}
	return "Нет подтверждённого обновления"
}
func displayTime(raw string, loc *time.Location) string {
	t, e := time.Parse(time.RFC3339Nano, raw)
	if e != nil {
		return "нет данных"
	}
	return t.In(loc).Format("02.01.2006 15:04")
}
func shortStock(v string) string {
	r := []rune(label(v))
	units := 0
	for i, c := range r {
		n := 1
		if c > 0xffff {
			n = 2
		}
		if units+n > 24 {
			return string(r[:i]) + "…"
		}
		units += n
	}
	return string(r)
}

func (s *Service) StockSourcesText(sc Scope, kind string, offset int) (string, error) {
	if kind != "stocks" && kind != "seller_stocks" && kind != "wb_stocks" {
		return "", ErrInput
	}
	if e := authorize(s.db, sc, auth.MarketplaceRead); e != nil {
		return "", e
	}
	c, e := connection(s.db, sc, false)
	if e != nil {
		return "", e
	}
	sc.ConnectionID = c.ID
	zone := "Europe/Moscow"
	_ = s.db.QueryRow(`SELECT timezone FROM workshop_settings WHERE workshop_id=?`, sc.WorkshopID).Scan(&zone)
	loc, e := time.LoadLocation(zone)
	if e != nil {
		loc = time.UTC
	}
	var out strings.Builder
	out.WriteString("Wildberries · Остатки\nСклад мастерской учитывается отдельно.\n")
	for _, k := range []string{"seller_stocks", "wb_stocks"} {
		if kind != "stocks" && kind != k {
			continue
		}
		p, e := s.StockPage(sc, k, offset)
		if e != nil {
			return "", e
		}
		source, title := "SELLER", "ОСТАТКИ ПРОДАВЦА"
		if k == "wb_stocks" {
			source, title = "WB", "ОСТАТКИ НА СКЛАДАХ WB"
		}
		info := wb.StockInfo{}
		var raw, stamp, status string
		e = s.db.QueryRow(`SELECT info_json,captured_at,status FROM marketplace_stock_runs WHERE workshop_id=? AND connection_id=? AND source=? ORDER BY id DESC LIMIT 1`, sc.WorkshopID, c.ID, source).Scan(&raw, &stamp, &status)
		detailed := e == nil
		if e != nil && e != sql.ErrNoRows {
			return "", ErrStorage
		}
		if detailed && json.Unmarshal([]byte(raw), &info) != nil {
			return "", ErrStorage
		}
		var attempt, state, code, blocked, retry string
		_ = s.db.QueryRow(`SELECT started_at,state,error_code,blocked_by,retry_at FROM marketplace_sync WHERE workshop_id=? AND connection_id=? AND kind=?`, sc.WorkshopID, c.ID, k).Scan(&attempt, &state, &code, &blocked, &retry)
		fmt.Fprintf(&out, "\n%s\n", title)
		if !detailed {
			status = strings.ToUpper(state)
			out.WriteString("Исторический результат; подробные счётчики неизвестны.\n")
		}
		if attempt > stamp && state != "" {
			fmt.Fprintf(&out, "Текущая попытка: %s (%s).\n", stockStatus(state), displayTime(attempt, loc))
			if blocked != "" {
				out.WriteString("Новая загрузка источника не выполнялась: требуется проверка привязки кабинета.\n")
			}
			if detailed {
				out.WriteString("Ниже последний сохранённый результат.\n")
			}
		}
		fmt.Fprintf(&out, "Статус: %s\n", stockStatus(status))
		var last string
		_ = s.db.QueryRow(`SELECT COALESCE(MAX(fetched_at),'') FROM marketplace_stocks WHERE workshop_id=? AND connection_id=? AND kind=?`, sc.WorkshopID, c.ID, k).Scan(&last)
		fmt.Fprintf(&out, "Обновлено: %s\nПозиций: %d\n", displayTime(last, loc), p.Total)
		if status == "PARTIAL" && detailed {
			fmt.Fprintf(&out, "Получено: %d; сохранено: %d.\n", info.Received, info.Saved)
			if info.Missing > 0 {
				fmt.Fprintf(&out, "Не подтверждено позиций: %d. Это не нулевые остатки; предыдущие значения сохранены.\n", info.Missing)
			}
			if info.Invalid > 0 {
				fmt.Fprintf(&out, "Не прошли проверку: %d. Корректные записи сохранены.\n", info.Invalid)
			}
			if info.Missing == 0 && info.Invalid == 0 {
				out.WriteString("Источник вернул неполные данные. Показаны сохранённые значения.\n")
			}
		}
		if status == "PERMISSION_DENIED" {
			out.WriteString("Источник недоступен для текущего токена.\n")
		}
		if status == "RATE_LIMITED" && info.RetryAt != "" {
			fmt.Fprintf(&out, "Повтор не раньше: %s\n", displayTime(info.RetryAt, loc))
		}
		if status != "SUCCESS" && status != "SUCCEEDED" && p.Total > 0 {
			out.WriteString("Показаны последние известные значения.\n")
		}
		limit := len(p.Items)
		if kind == "stocks" && limit > 5 {
			limit = 5
		}
		for i, x := range p.Items[:limit] {
			fmt.Fprintf(&out, "\n%d. %s\n", offset+i+1, shortStock(x.ProductName))
			if x.VendorCode != "" {
				fmt.Fprintf(&out, "Артикул: %s\n", shortStock(x.VendorCode))
			}
			if x.Variant != "" {
				fmt.Fprintf(&out, "Размер: %s\n", shortStock(x.Variant))
			}
			fmt.Fprintf(&out, "Склад: %s\nОстаток: %d шт.\n", shortStock(x.WarehouseName), x.Quantity)
			if x.CapturedAt != last {
				fmt.Fprintf(&out, "Подтверждено: %s\n", displayTime(x.CapturedAt, loc))
			}
		}
		if limit == 0 {
			out.WriteString("Сохранённых позиций на этой странице нет. Это не подтверждение нулевых остатков.\n")
		} else {
			fmt.Fprintf(&out, "\nПоказано %d–%d из %d.\n", offset+1, offset+limit, p.Total)
		}
		if kind == "stocks" {
			fmt.Fprintf(&out, "Полный список: /wb %s\n", k)
		} else {
			if offset > 0 {
				prev := offset - StockPageSize
				if prev < 0 {
					prev = 0
				}
				fmt.Fprintf(&out, "Назад: /wb %s %d\n", k, prev)
			}
			if offset+limit < p.Total {
				fmt.Fprintf(&out, "Далее: /wb %s %d\n", k, offset+limit)
			}
		}
	}
	return out.String(), nil
}
