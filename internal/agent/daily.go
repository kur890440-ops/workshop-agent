package agent

import (
	"errors"
	"strings"
	"time"
	"workshop-agent/internal/auth"
	"workshop-agent/internal/personalization"
)

func IsDailySummary(text string) bool {
	switch strings.ToLower(strings.TrimSpace(text)) {
	case "/summary", "/summary today", "сводка", "сводка за сегодня", "сколько сегодня заказов собрано", "сколько сегодня сделали", "что сегодня произвели", "📊 сводка за сегодня":
		return true
	}
	return false
}

func (a *WorkshopAgent) DailySummary(user, workshop int64, now time.Time) (string, error) {
	r, err := a.Prod.ForUser(user).DailyProduction(workshop, now)
	if err != nil {
		return "", err
	}
	answer := r.Text()
	// Preserve the existing material stock block, with its own permission check.
	materials, err := a.Inv.ForUser(user).ListMaterials(workshop)
	if errors.Is(err, auth.ErrDenied) {
		return answer + "\n\nМатериалы: нет прав для просмотра.", nil
	}
	if err != nil {
		return "", err
	}
	profile, err := personalization.New(a.WS.DB()).ForUser(user).GetProfile()
	if err != nil {
		return "", err
	}
	return answer + "\n\n" + RenderMaterialReport(materials, profile), nil
}
