package telegram

import (
	"context"
	"fmt"
	"time"
	"workshop-agent/internal/background"
)

// The adapter is implemented by MCPManager. Telegram never reads aggregate SQL.
type dailySummaryReader interface {
	DailySummary(context.Context, int64, int64) (background.DailySummary, error)
}

func (b *Bot) backgroundSummary(a buttonAction, trace bool) error {
	if b.DailySummary == nil {
		return b.sendMessage(a.Key.ChatID, "Чтение сводки MCP не настроено.")
	}
	parent := b.JobContext
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	out, e := b.DailySummary.DailySummary(ctx, a.Key.UserID, a.Workshop)
	if e != nil {
		return b.sendMessage(a.Key.ChatID, "Не удалось получить сохранённую сводку. Проверьте доступ к мастерской и повторите запрос.")
	}
	text := out.Summary
	if trace {
		text += fmt.Sprintf("\n\nMCP TRACE\nSource: Telegram «Последняя сводка»\nTool: wb_get_daily_summary\nTransport: in-memory\nSource data: BackgroundJobRun.aggregate_json\nRun ID: %d\nResult: %s / %s\nWB API CALLS: 0", out.RunID, out.Code, out.Status)
	}
	return b.sendMessage(a.Key.ChatID, text)
}
