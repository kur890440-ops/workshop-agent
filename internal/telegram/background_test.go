package telegram

import (
	"context"
	"strings"
	"testing"
	"workshop-agent/internal/background"
)

func TestBackgroundMenuAndNotificationScope(t *testing.T) {
	h, u, w := wbFixture(t)
	if _, e := h.bot.WS.DB().Exec(`INSERT INTO marketplace_connections(id,workshop_id,enabled,seller_id) VALUES(1,?,1,'fixture')`, w); e != nil {
		t.Fatal(e)
	}
	h.bot.Background = background.New(h.bot.WS.DB(), nil, h.bot)
	h.message(t, 900001, "/wb_auto enable")
	if !strings.Contains(h.sent[len(h.sent)-1]["text"].(string), "Europe/Moscow") {
		t.Fatal(h.sent)
	}
	h.click(t, 900001, "⏸ Пауза")
	j, _ := h.bot.Background.Get(u, w)
	if j.Status != "paused" {
		t.Fatal(j)
	}
	h.click(t, 900001, "▶ Возобновить")
	j, _ = h.bot.Background.Get(u, w)
	if j.Status != "active" {
		t.Fatal(j)
	}
	h.message(t, 900001, "/wb_auto time 09:30 Asia/Yekaterinburg 7")
	j, _ = h.bot.Background.Get(u, w)
	if j.LocalTime != "09:30" || j.Timezone != "Asia/Yekaterinburg" {
		t.Fatal(j)
	}
	h.click(t, 900001, "❌ Отключить")
	j, _ = h.bot.Background.Get(u, w)
	if j.Status != "cancelled" {
		t.Fatal(j)
	}
	if _, e := h.bot.WS.DB().Exec(`UPDATE workshop_members SET is_active=0 WHERE user_id=? AND workshop_id=?`, u, w); e != nil {
		t.Fatal(e)
	}
	before := len(h.sent)
	if e := h.bot.Send(context.Background(), u, w, "private summary"); e == nil || len(h.sent) != before {
		t.Fatal("revoked recipient received notification")
	}
}
