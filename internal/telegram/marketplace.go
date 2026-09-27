package telegram

import (
	"strconv"
	"strings"

	"workshop-agent/internal/auth"
	"workshop-agent/internal/marketplace"
)

const wbHelp = "WB: /wb — статус и кнопки.\n/wb stock_debug [seller_stocks|wb_stocks] [смещение] — технические остатки для владельца\n/wb debug — диагностика для владельца (без запросов WB)\n/wb history [смещение] — история WB\n/wb incident [смещение] — последний HTTP429 с предысторией\n/wb cards [смещение] — карточки, варианты и сопоставления\n/wb stocks — обновить оба источника остатков\n/wb seller_stocks [смещение] — сохранённые остатки продавца\n/wb wb_stocks [смещение] — сохранённые остатки складов WB\n/wb orders [смещение] — импортированные заказы\n/wb sync [catalog|seller_stocks|wb_stocks|orders|all]\n/wb check — обновить информацию о кабинете\n/wb attach, /wb disable — управление владельцем\n/wb map product_id nmID chrtID [barcode] — явное сопоставление владельцем\nТокены через Telegram не принимаются. Задайте WB_API_TOKEN локально и перезапустите приложение."

func (b *Bot) wbMessage(key sessionKey, text string) (bool, error) {
	fields := strings.Fields(text)
	if len(fields) == 0 || fields[0] != "/wb" {
		return false, nil
	}
	if b.Marketplace == nil {
		return true, b.sendMessage(key.ChatID, "Модуль WB не настроен.")
	}
	workshop, err := b.WS.ActiveWorkshop(key.UserID)
	if err != nil {
		return true, err
	}
	sc := marketplace.Scope{UserID: key.UserID, WorkshopID: workshop}
	c, err := b.Marketplace.Status(sc)
	if err != nil {
		return true, err
	}
	sc.ConnectionID = c.ID
	action := "status"
	if len(fields) > 1 {
		action = fields[1]
	}
	if action == "history" || action == "incident" {
		offset := 0
		if len(fields) > 3 {
			return true, marketplace.ErrInput
		}
		if len(fields) == 3 {
			offset, err = strconv.Atoi(fields[2])
			if err != nil {
				return true, marketplace.ErrInput
			}
		}
		text, e := b.Marketplace.TraceHistoryText(sc, action == "incident", offset)
		if e != nil {
			return true, e
		}
		return true, b.sendMessage(key.ChatID, text)
	}
	if action == "stock_debug" {
		kind := "seller_stocks"
		offset := 0
		if len(fields) > 2 {
			kind = fields[2]
		}
		if kind != "seller_stocks" && kind != "wb_stocks" {
			return true, marketplace.ErrInput
		}
		if len(fields) > 4 {
			return true, marketplace.ErrInput
		}
		if len(fields) == 4 {
			offset, err = strconv.Atoi(fields[3])
			if err != nil {
				return true, marketplace.ErrInput
			}
		}
		text, e := b.Marketplace.StockDebugText(sc, kind, offset)
		if e != nil {
			return true, e
		}
		return true, b.sendMessage(key.ChatID, text)
	}
	if action == "debug" {
		if len(fields) != 2 {
			return true, marketplace.ErrInput
		}
		text, e := b.Marketplace.DiagnosticsText(sc)
		if e != nil {
			return true, e
		}
		return true, b.sendMessage(key.ChatID, text)
	}
	if action == "help" {
		return true, b.sendMessage(key.ChatID, wbHelp)
	}
	if action == "map" {
		if len(fields) < 5 || len(fields) > 6 {
			return true, marketplace.ErrInput
		}
		ids := make([]int64, 3)
		for i := range ids {
			ids[i], err = ParseInternalID(fields[i+2])
			if err != nil {
				return true, marketplace.ErrInput
			}
		}
		barcode := ""
		if len(fields) == 6 {
			barcode = fields[5]
		}
		if err = b.Marketplace.SetMapping(sc, ids[0], ids[1], ids[2], barcode); err != nil {
			return true, err
		}
		return true, b.sendMessage(key.ChatID, "Сопоставление сохранено. /wb cards")
	}
	a := buttonAction{Key: key, Workshop: workshop, Target: c.ID, Action: "wb_" + action}
	if action == "sync" {
		a.Value = "all"
		if len(fields) == 3 {
			a.Value = fields[2]
		} else if len(fields) > 3 {
			return true, marketplace.ErrInput
		}
	}
	if action == "cards" || action == "stocks" || action == "seller_stocks" || action == "wb_stocks" || action == "orders" {
		if len(fields) == 3 {
			a.Value = fields[2]
		} else if len(fields) > 3 {
			return true, marketplace.ErrInput
		}
	}
	if action == "attach" || action == "disable" {
		a.Action = "wb_confirm_" + action
	}
	if action == "status" || action == "check" || action == "attach" || action == "disable" {
		if len(fields) > 2 {
			return true, marketplace.ErrInput
		}
	}
	return true, b.executeButton(a)
}
func (b *Bot) wbButton(a buttonAction) error {
	if b.Marketplace == nil {
		return marketplace.ErrInput
	}
	sc := marketplace.Scope{UserID: a.Key.UserID, WorkshopID: a.Workshop, ConnectionID: a.Target}
	c, err := b.Marketplace.Status(sc)
	if err != nil {
		return err
	}
	action := strings.TrimPrefix(a.Action, "wb_")
	if action == "confirm_attach" || action == "confirm_disable" {
		if err := auth.Require(b.WS.DB(), sc.UserID, sc.WorkshopID, auth.MarketplaceManage); err != nil {
			return err
		}
		text := "Привязать единственный настроенный кабинет WB к этой мастерской?"
		next := "attach"
		if action == "confirm_disable" {
			text = "Отключить WB и остановить обновление? Сохранённые данные останутся."
			next = "disable"
		}
		return b.screen(a.Key, text, choice{Text: "Подтвердить", Action: "wb_" + next, Workshop: a.Workshop, Target: a.Target}, choice{Text: "Отмена", Action: "wb_status", Workshop: a.Workshop, Target: a.Target})
	}
	switch action {
	case "attach":
		_, err = b.Marketplace.Attach(sc)
	case "disable":
		err = b.Marketplace.Disable(sc)
	case "check", "sync":
		kind := action
		if action == "sync" {
			kind = a.Value
			if kind == "" {
				kind = "all"
			}
		}
		_, err = b.Marketplace.Start(sc, kind)
		if err != nil {
			return err
		}
		return b.sendMessage(a.Key.ChatID, "WB: загрузка запущена. Можно продолжать работу. Результат и ошибки: /wb.")
	case "status", "cards", "stocks", "seller_stocks", "wb_stocks", "orders":
		offset := 0
		if a.Value != "" {
			offset, err = strconv.Atoi(a.Value)
			if err != nil {
				return marketplace.ErrInput
			}
		}

		if action == "stocks" && offset == 0 {
			done, e := b.Marketplace.Start(sc, "stocks")
			if e != nil {
				return e
			}
			if e = b.sendMessage(a.Key.ChatID, "Обновляю остатки продавца и складов WB независимо. Пришлю результат текущей попытки."); e != nil {
				return e
			}
			b.wbWait.Add(1)
			go func() {
				defer b.wbWait.Done()
				<-done
				// Recheck active membership before showing any asynchronous data.
				text, e := b.Marketplace.ReadText(sc, "stocks", 0)
				if e == nil {
					_ = b.sendMessage(a.Key.ChatID, "Результат загрузки остатков.\n"+text)
				}
			}()
			return nil
		}
		text, e := b.Marketplace.ReadText(sc, action, offset)
		if e != nil {
			return e
		}
		choices := []choice{{Text: "Статус WB", Action: "wb_status", Workshop: a.Workshop, Target: c.ID}}
		if action == "seller_stocks" || action == "wb_stocks" {
			page, e := b.Marketplace.StockPage(sc, action, offset)
			if e != nil {
				return e
			}
			if offset > 0 {
				prev := offset - marketplace.StockPageSize
				if prev < 0 {
					prev = 0
				}
				choices = append(choices, choice{Text: "◀ Назад", Action: "wb_" + action, Workshop: a.Workshop, Target: c.ID, Value: strconv.Itoa(prev)})
			}
			if offset+len(page.Items) < page.Total {
				choices = append(choices, choice{Text: "Следующие 15 ▶", Action: "wb_" + action, Workshop: a.Workshop, Target: c.ID, Value: strconv.Itoa(offset + len(page.Items))})
			}
		}

		if b.DailySummary != nil {
			choices = append(choices, choice{Text: "Последняя сводка", Action: "bg_summary", Workshop: a.Workshop})
		}
		if b.Background != nil {
			choices = append(choices, choice{Text: "Автосинхронизация", Action: "bg_status", Workshop: a.Workshop})
		}
		if c.ID > 0 {
			choices = append(choices, choice{Text: "Обновить информацию о кабинете", Action: "wb_check", Workshop: a.Workshop, Target: c.ID}, choice{Text: "Обновить WB", Action: "wb_sync", Workshop: a.Workshop, Target: c.ID, Value: "all"}, choice{Text: "Карточки WB", Action: "wb_cards", Workshop: a.Workshop, Target: c.ID}, choice{Text: "Остатки продавца", Action: "wb_seller_stocks", Workshop: a.Workshop, Target: c.ID}, choice{Text: "Склады WB", Action: "wb_wb_stocks", Workshop: a.Workshop, Target: c.ID}, choice{Text: "Заказы WB", Action: "wb_orders", Workshop: a.Workshop, Target: c.ID})
		}
		if auth.Require(b.WS.DB(), sc.UserID, sc.WorkshopID, auth.MarketplaceManage) == nil {
			if c.ID == 0 || !c.Enabled {
				choices = append(choices, choice{Text: "Подключить WB", Action: "wb_confirm_attach", Workshop: a.Workshop, Target: c.ID})
			} else {
				choices = append(choices, choice{Text: "Отключить WB", Action: "wb_confirm_disable", Workshop: a.Workshop, Target: c.ID})
			}
		}
		return b.screen(a.Key, text, choices...)
	default:
		return marketplace.ErrInput
	}
	if err != nil {
		return err
	}
	return b.wbButton(buttonAction{Key: a.Key, Action: "wb_status", Workshop: a.Workshop})
}
