package telegram

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"
	q "workshop-agent/internal/marketplacequery"
)

func (b *Bot) marketQueryCommand(key sessionKey, text string) (bool, error) {
	f := strings.Fields(text)
	if len(f) == 0 || (f[0] != "/mcp_trace" && f[0] != "/ozon_map") {
		return false, nil
	}
	if b.MarketQuery == nil {
		return true, b.sendMessage(key.ChatID, "Обработчик запросов маркетплейсов недоступен.")
	}
	w, e := b.WS.ActiveWorkshop(key.UserID)
	if e != nil {
		return true, e
	}
	sc := q.Scope{UserID: key.UserID, WorkshopID: w}
	if f[0] == "/mcp_trace" {
		v, e := b.MarketQuery.Store.LastTrace(sc)
		if e != nil {
			return true, b.sendMessage(key.ChatID, "Нет доступной трассировки для текущего пользователя и мастерской.")
		}
		raw, _ := json.MarshalIndent(v, "", "  ")
		return true, b.sendMessage(key.ChatID, "MCP Orchestration Trace\n"+string(raw))
	}
	if len(f) != 3 {
		return true, b.sendMessage(key.ChatID, "Явное сопоставление: /ozon_map <SKU> <ID внутреннего товара>. Только владелец.")
	}
	sku, e1 := strconv.ParseInt(f[1], 10, 64)
	id, e2 := strconv.ParseInt(f[2], 10, 64)
	if e1 != nil || e2 != nil || sku <= 0 || id <= 0 {
		return true, b.sendMessage(key.ChatID, "Нужны положительные целые SKU и ID товара.")
	}
	if e = b.MarketQuery.Store.MapOzon(sc, sku, id); e != nil {
		return true, b.sendMessage(key.ChatID, "Сопоставление не сохранено: проверьте права, SKU и принадлежность товара мастерской.")
	}
	return true, b.sendMessage(key.ChatID, "Сопоставление Ozon с внутренним товаром сохранено.")
}
func (b *Bot) executeMarketQuery(key sessionKey, workshop int64, text string, i q.MarketplaceQueryIntent) error {
	if b.MarketQuery == nil {
		return b.sendMessage(key.ChatID, "Обработчик маркетплейсов недоступен.")
	}
	sc := q.Scope{UserID: key.UserID, WorkshopID: workshop}
	if e := b.MarketQuery.Store.Authorize(sc); e != nil {
		return e
	}
	if e := b.sendMessage(key.ChatID, "Проверяю выбранные источники маркетплейсов. Результат пришлю отдельно."); e != nil {
		return e
	}
	parent := b.JobContext
	if parent == nil {
		parent = context.Background()
	}
	b.wbWait.Add(1)
	go func() {
		defer b.wbWait.Done()
		ctx, cancel := context.WithTimeout(parent, 3*time.Minute)
		defer cancel()
		r, e := b.MarketQuery.Execute(ctx, sc, text, i)
		if b.MarketQuery.Store.Authorize(sc) != nil {
			return
		}
		if e != nil {
			_ = b.sendMessage(key.ChatID, "Не удалось выполнить запрос. Проверьте доступ и явно укажите порог для низкого остатка.")
			return
		}
		_ = b.sendMessage(key.ChatID, q.Render(r))
	}()
	return nil
}
