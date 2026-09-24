package telegram

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"workshop-agent/internal/auth"
	"workshop-agent/internal/background"
)

// Send is the background notification boundary. user is an internal identity,
// never an arbitrary Telegram target from job parameters.
func (b *Bot) Send(ctx context.Context, user, workshop int64, text string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if e := auth.Require(b.WS.DB(), user, workshop, auth.MarketplaceRead); e != nil {
		return e
	}
	var chat int64
	if e := b.WS.DB().QueryRow(`SELECT telegram_user_id FROM users WHERE id=?`, user).Scan(&chat); e != nil || chat <= 0 {
		return auth.ErrDenied
	}
	return b.api("sendMessage", map[string]any{"chat_id": chat, "text": text}, nil)
}
func (b *Bot) backgroundMessage(key sessionKey, text string) (bool, error) {
	f := strings.Fields(text)
	if len(f) == 0 || f[0] != "/wb_auto" {
		return false, nil
	}
	if b.Background == nil {
		return true, b.sendMessage(key.ChatID, "Автосинхронизация не настроена.")
	}
	w, e := b.WS.ActiveWorkshop(key.UserID)
	if e != nil {
		return true, e
	}
	if len(f) == 3 && f[1] == "summary" && f[2] == "trace" {
		return true, b.backgroundButton(buttonAction{Key: key, Workshop: w, Action: "bg_summary_trace"})
	}
	if len(f) > 1 && f[1] == "time" {
		if len(f) != 4 && len(f) != 5 {
			return true, b.sendMessage(key.ChatID, "/wb_auto time 08:00 Europe/Moscow [порог_остатка]")
		}
		var threshold int
		if e = b.WS.DB().QueryRow(`SELECT wb_low_stock_threshold FROM workshop_settings WHERE workshop_id=?`, w).Scan(&threshold); e != nil {
			return true, e
		}
		if len(f) == 5 {
			threshold, e = strconv.Atoi(f[4])
			if e != nil {
				return true, background.ErrInput
			}
		}
		if e = b.Background.Change(key.UserID, w, "time", f[2], f[3], threshold); e != nil {
			return true, e
		}
		return true, b.backgroundButton(buttonAction{Key: key, Workshop: w, Action: "bg_status"})
	}
	action := "status"
	if len(f) == 2 {
		action = f[1]
	} else if len(f) > 2 {
		return true, background.ErrInput
	}
	return true, b.backgroundButton(buttonAction{Key: key, Workshop: w, Action: "bg_" + action})
}
func (b *Bot) backgroundButton(a buttonAction) error {
	if b.Background == nil {
		return background.ErrScope
	}
	active, e := b.WS.ActiveWorkshop(a.Key.UserID)
	if e != nil || active != a.Workshop {
		return auth.ErrDenied
	}
	action := strings.TrimPrefix(a.Action, "bg_")
	switch action {
	case "summary", "summary_trace":
		return b.backgroundSummary(a, action == "summary_trace")
	case "trace":
		text, e := b.Background.WBTrace(a.Key.UserID, a.Workshop)
		if e != nil {
			return e
		}
		return b.sendMessage(a.Key.ChatID, text)
	case "enable":
		_, e = b.Background.Create(a.Key.UserID, a.Workshop)
	case "pause", "resume", "cancel":
		e = b.Background.Change(a.Key.UserID, a.Workshop, action, "", "", 0)
	case "time":
		return b.sendMessage(a.Key.ChatID, "Изменить время, timezone и порог:\n/wb_auto time 08:00 Europe/Moscow 5")
	case "run":
		if e = auth.Require(b.WS.DB(), a.Key.UserID, a.Workshop, auth.MarketplaceManage); e != nil {
			return e
		}
		b.wbWait.Add(1)
		go func() {
			defer b.wbWait.Done()
			parent := b.JobContext
			if parent == nil {
				parent = context.Background()
			}
			ctx, cancel := context.WithTimeout(parent, 5*time.Minute)
			defer cancel()
			if err := b.Background.RunNow(ctx, a.Key.UserID, a.Workshop); err != nil {
				_ = b.Send(ctx, a.Key.UserID, a.Workshop, publicError(err))
			}
		}()
		return b.sendMessage(a.Key.ChatID, "Автосинхронизация: запрос запуска принят. Сводка придёт после выполнения; повтор за ту же дату не создаётся.")
	case "status":
	default:
		return background.ErrInput
	}
	if e != nil {
		return e
	}
	j, e := b.Background.Get(a.Key.UserID, a.Workshop)
	if e != nil {
		return e
	}
	choices := []choice{}
	can := auth.Require(b.WS.DB(), a.Key.UserID, a.Workshop, auth.MarketplaceManage) == nil
	if j.ID == 0 {
		if can {
			choices = append(choices, choice{Text: "Включить DAILY 08:00", Action: "bg_enable", Workshop: a.Workshop})
		}
		return b.screen(a.Key, "WB · Автосинхронизация выключена.\nНачальная timezone: Europe/Moscow. После включения её можно изменить.", choices...)
	}
	loc, e := time.LoadLocation(j.Timezone)
	if e != nil {
		return background.ErrInput
	}
	stamp := func(v int64) string {
		if v == 0 {
			return "не было"
		}
		return time.Unix(v, 0).In(loc).Format("02.01.2006 15:04")
	}
	text := fmt.Sprintf("WB · Автосинхронизация\nСтатус: %s\nВремя: %s\nTimezone: %s\nПоследний запуск: %s\nПоследний результат: %s\nСледующий запуск: %s", j.Status, j.LocalTime, j.Timezone, stamp(j.LastRun), j.LastResult, stamp(j.NextRun))
	if can && (j.Status == "active" || j.Status == "paused") {
		choices = append(choices, choice{Text: "▶ Запустить сейчас", Action: "bg_run", Workshop: a.Workshop})
		if j.Status == "active" {
			choices = append(choices, choice{Text: "⏸ Пауза", Action: "bg_pause", Workshop: a.Workshop})
		} else {
			choices = append(choices, choice{Text: "▶ Возобновить", Action: "bg_resume", Workshop: a.Workshop})
		}
		choices = append(choices, choice{Text: "🕗 Изменить время", Action: "bg_time", Workshop: a.Workshop}, choice{Text: "❌ Отключить", Action: "bg_cancel", Workshop: a.Workshop})
	}
	if j.Status != "active" {
		text += "\nРасписание сейчас не выполняется."
	}
	return b.screen(a.Key, text, choices...)
}
func backgroundError(err error) string {
	var e background.Error
	if errors.As(err, &e) {
		return e.Error()
	}
	return ""
}
