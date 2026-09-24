package marketplace

import (
	"fmt"
	"strings"
	"time"
	"unicode"
	wb "workshop-agent/internal/marketplace/wildberries"
)

// External fields are plain bounded data. They are never passed to an LLM or parsed as commands.
func label(v string) string {
	v = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, v)
	r := []rune(v)
	if len(r) > 60 {
		return string(r[:60]) + "…"
	}
	return v
}
func (s *Service) ReadText(sc Scope, kind string, offset int) (string, error) {
	c, err := s.Status(sc)
	if err != nil {
		return "", err
	}
	lines := []string{fmt.Sprintf("WB · мастерская %d · подключение %d", sc.WorkshopID, c.ID)}
	title, valid := map[string]string{"status": "Статус WB", "cards": "Карточки WB", "stocks": "Остатки WB", "orders": "Заказы WB (FBS)"}[kind]
	if !valid {
		return "", ErrInput
	}
	lines = append([]string{title}, lines...)
	if !c.Configured {
		lines = append(lines, "WB_API_TOKEN не настроен. Владелец задаёт его локально, затем перезапускает приложение.")
	}
	if c.ID == 0 {
		return strings.Join(append(lines, "Кабинет не привязан. Подключение выполняет владелец."), "\n"), nil
	}
	sc.ConnectionID = c.ID
	if c.Enabled {
		lines = append(lines, "Подключение включено.")
	} else {
		lines = append(lines, "Подключение отключено. Ниже только сохранённые данные.")
	}
	if c.SellerID != "" {
		lines = append(lines, "Кабинет: "+label(c.SellerName)+" · "+label(c.SellerID))
	}
	loaded := false
	for _, r := range c.Sync {
		relevant := kind == "status" || kind == "cards" && r.Kind == "catalog" || kind == "stocks" && (r.Kind == "wb_stocks" || r.Kind == "seller_stocks") || kind == "orders" && r.Kind == "orders"
		if !relevant && r.Kind != "check" {
			continue
		}
		if relevant && r.Kind != "check" && r.LastSuccess != "" {
			loaded = true
		}
		last := r.LastSuccess
		if last == "" {
			last = "не было"
		}
		lines = append(lines, fmt.Sprintf("%s: %s; полный успех: %s; ошибка: %s", r.Kind, r.State, last, r.ErrorCode))
		lines = append(lines, "Начало попытки: "+r.StartedAt+"; завершение: "+r.FinishedAt)
		if r.BlockedBy != "" {
			lines = append(lines, r.Kind+": запрос не выполнялся — не пройдена проверка кабинета (seller-info).")
		}
		if r.RetryAt != "" {
			if at, e := time.Parse(time.RFC3339Nano, r.RetryAt); e == nil {
				rate := wb.RateLimitError{Operation: r.RateOperation, RetryAt: at, Source: r.RetrySource}
				lines = append(lines, rate.Message())
			}
		} else if r.ErrorCode == "rate_limited" {
			lines = append(lines, "Срок ограничения WB неизвестен.")
		}
	}
	if kind == "status" {
		return strings.Join(lines, "\n"), nil
	}
	lines = append(lines, "Источник: сохранённые данные Wildberries.")
	count := 0
	switch kind {
	case "cards":
		v, e := s.ListVariants(sc, offset)
		if e != nil {
			return "", e
		}
		count = len(v)
		for _, x := range v {
			lines = append(lines, fmt.Sprintf("nm=%d chrt=%d barcode=%s → наш #%d (%s) · %s", x.NmID, x.ChrtID, label(x.Barcode), x.ProductID, x.MappingStatus, label(x.Title)))
		}
	case "stocks":
		v, e := s.ListStocks(sc, offset)
		if e != nil {
			return "", e
		}
		count = len(v)
		for _, x := range v {
			lines = append(lines, fmt.Sprintf("%s склад %d · nm=%d chrt=%d: %d", x.Kind, x.WarehouseID, x.NmID, x.ChrtID, x.Quantity))
		}
	case "orders":
		v, e := s.ListOrders(sc, offset)
		if e != nil {
			return "", e
		}
		count = len(v)
		for _, x := range v {
			lines = append(lines, fmt.Sprintf("Заказ #%d · nm=%d chrt=%d · %s / %s", x.ID, x.NmID, x.ChrtID, label(x.SupplierStatus), label(x.WBStatus)))
		}
	default:
		return "", ErrInput
	}
	if count == 0 {
		switch kind {
		case "stocks":
			lines = append(lines, "Строк нет. Это не подтверждение нулевых остатков; проверьте состояние загрузки.")
		case "orders":
			if !loaded {
				lines = append(lines, "Успешной загрузки заказов ещё не было. Это не означает, что заказов в WB нет.")
			} else {
				lines = append(lines, "На этой странице сохранённой выборки заказов нет.")
			}
			lines = append(lines, "Загрузить новые FBS-заказы и обновить статусы: /wb sync orders. Состояние загрузки: /wb.")
		case "cards":
			lines = append(lines, "На этой странице сохранённых карточек нет. Обновить: /wb sync catalog.")
		}
	}
	if count == 10 {
		lines = append(lines, fmt.Sprintf("Следующая страница: /wb %s %d", kind, offset+10))
	}
	return strings.Join(lines, "\n"), nil
}
