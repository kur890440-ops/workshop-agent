package marketplace

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"workshop-agent/internal/auth"
	wb "workshop-agent/internal/marketplace/wildberries"
)

func (s *Service) StockDebugText(sc Scope, kind string, offset int) (string, error) {
	if offset < 0 || offset > 10000 {
		return "", ErrInput
	}
	if e := authorize(s.db, sc, auth.MarketplaceManage); e != nil {
		return "", e
	}
	c, e := connection(s.db, sc, false)
	if e != nil {
		return "", e
	}
	sc.ConnectionID = c.ID
	var text strings.Builder
	text.WriteString("Wildberries · Остатки\nСклад мастерской отдельно; источники не суммируются.\n")
	for _, source := range []wb.StockSource{wb.StockSeller, wb.StockWB} {
		k, title := "seller_stocks", "ОСТАТКИ ПРОДАВЦА"
		if source == wb.StockWB {
			k = "wb_stocks"
			title = "ОСТАТКИ НА СКЛАДАХ WB"
		}
		if kind != "stocks" && kind != k {
			continue
		}
		info := wb.StockInfo{Source: source, Status: "UNKNOWN"}
		var raw, stamp, status string
		detailed := false
		e = s.db.QueryRow(`SELECT info_json,captured_at,status FROM marketplace_stock_runs WHERE workshop_id=? AND connection_id=? AND source=? ORDER BY id DESC LIMIT 1`, sc.WorkshopID, c.ID, source).Scan(&raw, &stamp, &status)
		if e == nil {
			detailed = true
			if json.Unmarshal([]byte(raw), &info) != nil {
				return "", ErrStorage
			}
			info.Status = status
		} else if e != sql.ErrNoRows {
			return "", ErrStorage
		} else {
			var st, code string
			if s.db.QueryRow(`SELECT state,error_code,started_at FROM marketplace_sync WHERE workshop_id=? AND connection_id=? AND kind=?`, sc.WorkshopID, c.ID, k).Scan(&st, &code, &info.StartedAt) == nil {
				info.Status = strings.ToUpper(st)
				info.Error = code
				if code == "forbidden" {
					info.Status = "PERMISSION_DENIED"
				}
				if code == "rate_limited" {
					info.Status = "RATE_LIMITED"
				}
			}
		}
		var blocked, retry string
		_ = s.db.QueryRow(`SELECT blocked_by,retry_at FROM marketplace_sync WHERE workshop_id=? AND connection_id=? AND kind=?`, sc.WorkshopID, c.ID, k).Scan(&blocked, &retry)
		if blocked != "" {
			fmt.Fprintf(&text, "%s: запрос не выполнялся — зависимость %s; Начало попытки: %s; повтор: %s\n", title, blocked, info.StartedAt, retry)
		}
		var attempt, state string
		_ = s.db.QueryRow(`SELECT started_at,state FROM marketplace_sync WHERE workshop_id=? AND connection_id=? AND kind=?`, sc.WorkshopID, c.ID, k).Scan(&attempt, &state)
		if !detailed {
			fmt.Fprintf(&text, "\n%s: исторический результат; подробные счётчики неизвестны. Новая загрузка источника не завершена.\n", title)
		} else if attempt > info.StartedAt && attempt > stamp {
			fmt.Fprintf(&text, "Текущая попытка %s: %s. Ниже предыдущий сохранённый результат.\n", attempt, state)
		}
		var lastSuccess, lastSnapshot string
		_ = s.db.QueryRow(`SELECT COALESCE(MAX(captured_at),'') FROM marketplace_stock_runs WHERE workshop_id=? AND connection_id=? AND source=? AND status='SUCCESS'`, sc.WorkshopID, c.ID, source).Scan(&lastSuccess)
		_ = s.db.QueryRow(`SELECT COALESCE(MAX(r.captured_at),'') FROM marketplace_stock_runs r WHERE workshop_id=? AND connection_id=? AND source=? AND EXISTS(SELECT 1 FROM marketplace_stock_snapshots x WHERE x.run_id=r.id)`, sc.WorkshopID, c.ID, source).Scan(&lastSnapshot)
		fmt.Fprintf(&text, "\n%s\nСтатус: %s\nПоследняя попытка: %s\nПолный успех: %s\nСнимок: %s\n", title, info.Status, info.StartedAt, lastSuccess, lastSnapshot)
		if detailed {
			fmt.Fprintf(&text, "Получено: %d; валидных: %d; сохранено: %d; ошибочных: %d; отсутствует: %d\n", info.Received, info.Valid, info.Saved, info.Invalid, info.Missing)
		}

		if info.Status == "PERMISSION_DENIED" {
			text.WriteString("Остатки этого источника недоступны для текущего токена (403).\n")
		}
		if info.Status == "PARTIAL" {
			text.WriteString("Частичная выборка. Отсутствующие позиции неизвестны, прежние значения сохранены.\n")
		}
		if info.Error != "" {
			fmt.Fprintf(&text, "Причина: %s; повтор не раньше: %s\n", label(info.Error), info.RetryAt)
		}
		rows, e := s.db.Query(`SELECT warehouse_id,nm_id,chrt_id,quantity,fetched_at FROM marketplace_stocks WHERE workshop_id=? AND connection_id=? AND kind=? ORDER BY warehouse_id,nm_id,chrt_id LIMIT 5 OFFSET ?`, sc.WorkshopID, c.ID, k, offset)
		if e != nil {
			return "", ErrStorage
		}
		n := 0
		for rows.Next() {
			var wh, nm, ch, qty int64
			var at string
			if rows.Scan(&wh, &nm, &ch, &qty, &at) != nil {
				rows.Close()
				return "", ErrStorage
			}
			n++
			fmt.Fprintf(&text, "Склад %d · nm=%d / %d: %d (снимок %s)\n", wh, nm, ch, qty, at)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return "", ErrStorage
		}
		if n == 0 {
			text.WriteString("Сохранённых данных на этой странице нет; это не нулевой остаток.\n")
		} else if info.Status != "SUCCESS" {
			text.WriteString("Показаны ранее сохранённые/частичные данные с датой каждой записи.\n")
		}
		if n == 5 {
			fmt.Fprintf(&text, "Далее: /wb stock_debug %s %d\n", k, offset+5)
		}
	}
	return text.String(), nil
}
