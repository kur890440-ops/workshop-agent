package telegram

import (
	"context"
	"strings"
	"time"
	"workshop-agent/internal/marketplace"
)

func (b *Bot) mcpStocksMessage(key sessionKey, text string) (bool, error) {
	f := strings.Fields(text)
	if len(f) == 0 || f[0] != "/wb_stocks" {
		return false, nil
	}
	if len(f) > 2 || len(f) == 2 && f[1] != "trace" {
		return true, b.sendMessage(key.ChatID, "Используйте /wb_stocks или /wb_stocks trace.")
	}
	if b.Agent == nil {
		return true, b.sendMessage(key.ChatID, "MCP не настроен.")
	}
	workshop, err := b.WS.ActiveWorkshop(key.UserID)
	if err != nil {
		return true, err
	}
	if b.Marketplace == nil {
		return true, marketplace.ErrScope
	}
	scope := marketplace.Scope{UserID: key.UserID, WorkshopID: workshop}
	connection, err := b.Marketplace.Status(scope)
	if err != nil {
		return true, err
	}
	if connection.ID == 0 || !connection.Enabled {
		return true, marketplace.ErrDisabled
	}
	scope.ConnectionID = connection.ID
	if err = b.sendMessage(key.ChatID, "Запрашиваю остатки WB через MCP. Результат пришлю после загрузки."); err != nil {
		return true, err
	}
	b.wbWait.Add(1)
	go func() {
		defer b.wbWait.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 115*time.Second)
		defer cancel()
		answer, _, err := b.Agent.MCPStocks(ctx, key.UserID, workshop, len(f) == 2)
		if err != nil {
			answer = publicError(err)
		}
		// The service revalidates membership/revision. Check the active workshop
		// again at delivery and suppress a response after the user switched away.
		active, e := b.WS.ActiveWorkshop(key.UserID)
		if e != nil || active != workshop {
			return
		}
		if err == nil {
			current, denied := b.Marketplace.Status(scope)
			if denied != nil || !current.Enabled || current.Revision != connection.Revision {
				return
			}
		}
		_ = b.api("sendMessage", map[string]interface{}{"chat_id": key.ChatID, "text": answer}, nil)
	}()
	return true, nil
}
