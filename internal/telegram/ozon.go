package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"workshop-agent/internal/auth"
	"workshop-agent/internal/background"
	"workshop-agent/internal/marketplace/ozon"
)

type ozonTools interface {
	Ozon(context.Context, ozon.Access, string, ozon.Input) (ozon.Result, error)
	OzonDiscovery() string
}

const ozonHelp = "Ozon: /ozon\n/ozon attach — привязать кабинет (владелец)\n/ozon disable — отключить\n/ozon products [смещение]\n/ozon seller_stocks [смещение]\n/ozon fbo_stocks [смещение]\n/ozon refresh catalog|seller|fbo\n/ozon debug — диагностика (владелец).\nCredentials задаются только локально: OZON_CLIENT_ID и OZON_API_KEY; затем перезапуск."

func (b *Bot) ozonMessage(key sessionKey, text string) (bool, error) {
	f := strings.Fields(text)
	if len(f) == 0 || f[0] != "/ozon" {
		return false, nil
	}
	if len(f) > 3 {
		return true, b.sendMessage(key.ChatID, ozonHelp)
	}
	w, e := b.WS.ActiveWorkshop(key.UserID)
	if e != nil {
		return true, e
	}
	action := "status"
	value := ""
	if len(f) > 1 {
		action = f[1]
	}
	if len(f) > 2 {
		value = f[2]
	}
	return true, b.ozonButton(buttonAction{Key: key, Workshop: w, Target: 2, Action: "ozon_" + action, Value: value})
}
func (b *Bot) ozonButton(a buttonAction) error {
	if b.Ozon == nil || b.OzonTools == nil {
		return b.sendMessage(a.Key.ChatID, "Модуль Ozon не настроен.")
	}
	sc := ozon.Access{UserID: a.Key.UserID, WorkshopID: a.Workshop, ConnectionID: 2}
	if a.Target != 0 && a.Target != 2 {
		return auth.ErrDenied
	}
	c, e := b.Ozon.Status(sc)
	if e != nil {
		return b.sendMessage(a.Key.ChatID, "Подключение Ozon недоступно в этой мастерской.")
	}
	action := strings.TrimPrefix(a.Action, "ozon_")
	switch action {
	case "help":
		return b.sendMessage(a.Key.ChatID, ozonHelp)
	case "attach":
		e = b.Ozon.Attach(sc)
		if e != nil {
			return b.sendMessage(a.Key.ChatID, "Не удалось привязать Ozon. Проверьте права владельца и локальную настройку credentials; скрытая смена кабинета запрещена.")
		}
		return b.ozonButton(buttonAction{Key: a.Key, Workshop: a.Workshop, Action: "ozon_status"})
	case "disable":
		if e = b.Ozon.Disable(sc); e != nil {
			return b.sendMessage(a.Key.ChatID, "Не удалось отключить Ozon: проверьте права владельца.")
		}
		return b.ozonButton(buttonAction{Key: a.Key, Workshop: a.Workshop, Action: "ozon_status"})
	case "debug":
		traces, e := b.Ozon.History(sc)
		if e != nil {
			return auth.ErrDenied
		}
		raw, _ := json.MarshalIndent(traces, "", "  ")
		for _, part := range background.SplitMorningSummary(b.OzonTools.OzonDiscovery() + "\nПоследние HTTP-вызовы и локальные блокировки:\n" + string(raw)) {
			if e = b.sendMessage(a.Key.ChatID, part); e != nil {
				return e
			}
		}
		return nil
	case "refresh":
		tool := ozonTool(a.Value)
		if tool == "" {
			return b.sendMessage(a.Key.ChatID, ozonHelp)
		}
		if c.ID == 0 || !c.Enabled {
			return b.sendMessage(a.Key.ChatID, "Сначала подключите кабинет Ozon.")
		}
		if e = b.sendMessage(a.Key.ChatID, "Ozon: обновление запущено. Результат пришлю после загрузки."); e != nil {
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
			out, err := b.OzonTools.Ozon(ozonContext(ctx, tool), sc, tool, ozon.Input{Refresh: true})
			current, e := b.Ozon.Status(sc)
			if e != nil || !current.Enabled || current.Revision != c.Revision || ctx.Err() != nil {
				return
			}
			text := "Не удалось обновить Ozon. Проверьте подключение и диагностику /ozon debug."
			if err == nil {
				text = ozon.Format(out)
			}
			for _, part := range background.SplitMorningSummary(text) {
				if _, e = b.Ozon.Status(sc); e != nil {
					return
				}
				if b.sendMessage(a.Key.ChatID, part) != nil {
					return
				}
			}
		}()
		return nil
	case "products", "seller_stocks", "fbo_stocks":
		offset := 0
		if a.Value != "" {
			offset, e = strconv.Atoi(a.Value)
			if e != nil || offset < 0 {
				return b.sendMessage(a.Key.ChatID, ozonHelp)
			}
		}
		out, e := b.OzonTools.Ozon(ozonContext(context.Background(), ozonTool(action)), sc, ozonTool(action), ozon.Input{Offset: offset})
		if e != nil {
			return b.sendMessage(a.Key.ChatID, "Не удалось прочитать сохранённые данные Ozon. Проверьте подключение /ozon.")
		}
		for _, part := range background.SplitMorningSummary(ozon.Format(out)) {
			if _, e = b.Ozon.Status(sc); e != nil {
				return e
			}
			if e = b.sendMessage(a.Key.ChatID, part); e != nil {
				return e
			}
		}
		return nil
	case "status":
		text := fmt.Sprintf("Ozon · мастерская %d\nНастроено локально: %t\nПодключение включено: %t\nПросмотр использует сохранённые данные; обновление запускается явно.", a.Workshop, c.Configured, c.Enabled)
		choices := []choice{}
		if c.ID != 0 {
			for _, v := range []struct{ text, action, value string }{{"Товары", "products", ""}, {"Остатки продавца", "seller_stocks", ""}, {"Остатки FBO Ozon", "fbo_stocks", ""}, {"Обновить каталог", "refresh", "catalog"}, {"Обновить остатки продавца", "refresh", "seller"}, {"Обновить FBO Ozon", "refresh", "fbo"}} {
				choices = append(choices, choice{Text: v.text, Action: "ozon_" + v.action, Workshop: a.Workshop, Target: 2, Value: v.value})
			}
		}
		if auth.Require(b.WS.DB(), sc.UserID, sc.WorkshopID, auth.MarketplaceManage) == nil {
			action := "attach"
			label := "Подключить Ozon"
			if c.Enabled {
				action = "disable"
				label = "Отключить Ozon"
			}
			choices = append(choices, choice{Text: label, Action: "ozon_" + action, Workshop: a.Workshop, Target: 2}, choice{Text: "Диагностика Ozon", Action: "ozon_debug", Workshop: a.Workshop, Target: 2})
		}
		return b.screen(a.Key, text, choices...)
	default:
		return b.sendMessage(a.Key.ChatID, ozonHelp)
	}
}
func ozonTool(action string) string {
	switch action {
	case "catalog", "products":
		return "ozon_list_products"
	case "seller", "seller_stocks":
		return "ozon_get_seller_stocks"
	case "fbo", "fbo_stocks":
		return "ozon_get_ozon_stocks"
	}
	return ""
}

func ozonContext(ctx context.Context, tool string) context.Context {
	caller := "telegram:ozon_products"
	if tool == "ozon_get_seller_stocks" {
		caller = "telegram:ozon_seller_stocks"
	}
	if tool == "ozon_get_ozon_stocks" {
		caller = "telegram:ozon_side_stocks"
	}
	return ozon.WithMetadata(ctx, ozon.Metadata{Caller: caller, Tool: tool, Cache: "BYPASS"})
}
