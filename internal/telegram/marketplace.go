package telegram

import (
	"log"
	"strconv"
	"strings"

	"workshop-agent/internal/auth"
	"workshop-agent/internal/marketplace"
)

const wbHelp = "WB: /wb — статус и кнопки.\n/wb cards [смещение] — карточки, варианты и сопоставления\n/wb stocks [смещение] — остатки продавца и WB\n/wb orders [смещение] — импортированные заказы\n/wb sync [catalog|seller_stocks|wb_stocks|orders|all]\n/wb check — проверить кабинет\n/wb attach, /wb disable — управление владельцем\n/wb map product_id nmID chrtID [barcode] — явное сопоставление владельцем\nТокены через Telegram не принимаются. Задайте WB_API_TOKEN локально и перезапустите приложение."

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
	if action == "cards" || action == "stocks" || action == "orders" {
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
	case "status", "cards", "stocks", "orders":
		offset := 0
		if a.Value != "" {
			offset, err = strconv.Atoi(a.Value)
			if err != nil {
				return marketplace.ErrInput
			}
		}
		if action == "stocks" && offset == 0 {
			return b.refreshWBStocks(a, sc)
		}
		text, e := b.Marketplace.ReadText(sc, action, offset)
		if e != nil {
			return e
		}
		choices := []choice{{Text: "Статус WB", Action: "wb_status", Workshop: a.Workshop, Target: c.ID}}
		if c.ID > 0 {
			choices = append(choices, choice{Text: "Проверить WB", Action: "wb_check", Workshop: a.Workshop, Target: c.ID}, choice{Text: "Обновить WB", Action: "wb_sync", Workshop: a.Workshop, Target: c.ID, Value: "all"}, choice{Text: "Карточки WB", Action: "wb_cards", Workshop: a.Workshop, Target: c.ID}, choice{Text: "Остатки WB", Action: "wb_stocks", Workshop: a.Workshop, Target: c.ID}, choice{Text: "Заказы WB", Action: "wb_orders", Workshop: a.Workshop, Target: c.ID})
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

func (b *Bot) refreshWBStocks(a buttonAction, sc marketplace.Scope) error {
	done, err := b.Marketplace.Start(sc, "wb_stocks")
	if err != nil {
		return err
	}
	if err := b.sendMessage(a.Key.ChatID, "Обновляю остатки на складах WB. После загрузки пришлю результат. Остатки складов продавца обновляются через «Обновить WB»."); err != nil {
		return err
	}
	b.wbWait.Add(1)
	go func() {
		defer b.wbWait.Done()
		<-done
		// Recheck scope and permissions after the network wait. Do not change
		// the current dialog or publish data from a previously active workshop.
		active, err := b.WS.ActiveWorkshop(sc.UserID)
		if err != nil || active != sc.WorkshopID {
			return
		}
		c, err := b.Marketplace.Status(sc)
		if err != nil || !c.Enabled {
			return
		}
		text, err := b.Marketplace.ReadText(sc, "stocks", 0)
		if err != nil {
			return
		}
		heading := "Обновление остатков WB не завершилось успешно. Ниже сохранённые данные и состояние загрузки."
		for _, state := range c.Sync {
			if state.Kind == "wb_stocks" && state.State == "succeeded" {
				heading = "Остатки на складах WB обновлены."
			}
		}
		// A plain message avoids changing form state from a background goroutine.
		if err := b.api("sendMessage", map[string]any{"chat_id": a.Key.ChatID, "text": heading + "\n" + text}, nil); err != nil {
			log.Print("WB stocks notification failed; result available via /wb")
		}
	}()
	return nil
}
