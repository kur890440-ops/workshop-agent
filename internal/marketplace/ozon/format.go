package ozon

import (
	"fmt"
	"strings"
	"time"
)

func short(s string, n int) string {
	r := []rune(clean(s))
	if len(r) > n {
		return string(r[:n]) + "…"
	}
	return string(r)
}
func StatusText(code string) string {
	switch code {
	case Success:
		return "Обновлено"
	case Partial:
		return "Получено частично; отсутствующие позиции неизвестны"
	case string(PermissionDenied):
		return "Нет доступа к этому источнику Ozon"
	case string(AuthFailed):
		return "Не удалось авторизоваться в Ozon"
	case string(RateLimited):
		return "Ozon временно ограничил запросы"
	case "UNAVAILABLE":
		return "Данные ещё не загружены"
	case string(InvalidResponse):
		return "Не удалось разобрать ответ Ozon"
	case string(BadRequest):
		return "Ozon отклонил запрос остатков или товаров (HTTP 400). Диагностика для владельца: /ozon debug"
	case string(NotConfigured):
		return "Подключение не настроено"
	default:
		return "Обновление не завершилось успешно"
	}
}

// Format uses normalized cached data only. External strings never become actions.
func Format(r Result) string {
	if r.Source == SellerSource {
		return formatSeller(r)
	}
	title := "Товары"
	command := "products"
	if r.Source == SellerSource {
		title = "Остатки продавца / FBS, rFBS"
		command = "seller_stocks"
	}
	if r.Source == FBOSource {
		title = "FBO Ozon · доступно к продаже (аналитика)"
		command = "fbo_stocks"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Ozon · %s\n%s\n", title, StatusText(r.Status))
	if r.CapturedAt != "" {
		fmt.Fprintf(&b, "Последняя попытка: %s\n", r.CapturedAt)
	}
	if r.LastSuccess != "" {
		fmt.Fprintf(&b, "Полный успех: %s\n", r.LastSuccess)
	}
	if r.RetryNotBefore != "" {
		fmt.Fprintf(&b, "Повтор после: %s\n", r.RetryNotBefore)
	}
	if r.Source == FBOSource {
		b.WriteString("Только SKU локального каталога; это аналитические данные, не онлайн-баланс.\n")
	}
	if r.Source != CatalogSource {
		b.WriteString("Склад мастерской и другие источники не суммируются.\n")
	}
	if r.Total == 0 {
		b.WriteString("Сохранённых строк нет. Это не подтверждение нулевых остатков.\n")
	}
	for i, p := range r.Products {
		name := p.Name
		if name == "" {
			name = "Товар " + p.OfferID
		}
		fmt.Fprintf(&b, "\n%d. %s\nАртикул: %s\n", r.Offset+i+1, short(name, 75), short(p.OfferID, 45))
	}
	for _, v := range r.Stocks {
		name := v.Name
		if name == "" {
			name = v.OfferID
		}
		if name == "" {
			name = fmt.Sprintf("SKU %d", v.SKU)
		}
		w := v.WarehouseName
		if w == "" {
			w = fmt.Sprint(v.WarehouseID)
		}
		label := "доступно"
		if v.Observation == "MISSING" || v.Observation == "STALE" {
			label = "ранее было; сейчас неизвестно"
		}
		fmt.Fprintf(&b, "\n%s · SKU %d\nСклад: %s · %s: %d\nСнимок: %s\n", short(name, 55), v.SKU, short(w, 32), label, v.Quantity, v.CapturedAt)
	}
	count := len(r.Products) + len(r.Stocks)
	if r.Offset+count < r.Total {
		fmt.Fprintf(&b, "\nДалее: /ozon %s %d", command, r.Offset+count)
	}
	return b.String()
}

func formatSeller(r Result) string {
	var b strings.Builder
	b.WriteString("Ozon · Остатки продавца\n" + StatusText(r.Status) + "\n")
	if stamp, e := time.Parse(time.RFC3339Nano, r.CapturedAt); e == nil {
		fmt.Fprintf(&b, "Последняя попытка: %s\n", stamp.In(time.Local).Format("02.01.2006 15:04 MST"))
	}
	if r.LastSuccess != "" {
		if stamp, e := time.Parse(time.RFC3339Nano, r.LastSuccess); e == nil {
			fmt.Fprintf(&b, "Обновлено полностью: %s\n", stamp.In(time.Local).Format("02.01.2006 15:04 MST"))
		}
	}
	if r.RetryNotBefore != "" {
		fmt.Fprintf(&b, "Повтор после: %s\n", r.RetryNotBefore)
	}
	if r.ErrorCode == "CATALOG_SKUS_REQUIRED" {
		b.WriteString("В каталоге нет пригодных SKU. Сначала обновите каталог: /ozon refresh catalog.\n")
	}
	if r.Status != Success {
		b.WriteString("Данные неполные или загрузка не удалась. Отсутствующие позиции не считаются нулевыми.\n")
	}
	groups := map[int64][]Stock{}
	order := []int64{}
	for _, s := range r.Stocks {
		if _, ok := groups[s.SKU]; !ok {
			order = append(order, s.SKU)
		}
		groups[s.SKU] = append(groups[s.SKU], s)
	}
	for _, sku := range order {
		rows := groups[sku]
		name := rows[0].Name
		if name == "" {
			name = rows[0].OfferID
		}
		if name == "" {
			name = fmt.Sprintf("Товар SKU %d", sku)
		}
		fmt.Fprintf(&b, "\n%s\n", short(name, 80))
		var sum int64
		confirmed := true
		for _, s := range rows {
			warehouse := s.WarehouseName
			if warehouse == "" {
				warehouse = fmt.Sprintf("Склад %d", s.WarehouseID)
			}
			known := (r.Status == Success || r.Status == Partial) && s.Observation != "MISSING" && s.Observation != "STALE"
			if known {
				fmt.Fprintf(&b, "• %s — %d шт.\n", short(warehouse, 50), s.Quantity)
				sum += s.Quantity
			} else {
				confirmed = false
				fmt.Fprintf(&b, "• %s — сейчас неизвестно; ранее %d шт.\n", short(warehouse, 50), s.Quantity)
			}
			if s.CapturedAt != "" {
				fmt.Fprintf(&b, "  Снимок: %s\n", s.CapturedAt)
			}
		}
		if confirmed && r.Status == Success && r.Total == len(r.Stocks) {
			fmt.Fprintf(&b, "Всего — %d шт.\n", sum)
		} else if confirmed {
			fmt.Fprintf(&b, "Подтверждено на этой странице — %d шт.\n", sum)
		}
	}
	if len(r.Stocks) == 0 {
		b.WriteString("Сохранённых строк нет. Это не подтверждение нулевых остатков.\n")
	}
	if r.Offset+len(r.Stocks) < r.Total {
		fmt.Fprintf(&b, "\nДалее: /ozon seller_stocks %d", r.Offset+len(r.Stocks))
	}
	return b.String()
}
