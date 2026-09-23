package agent

import (
	"strconv"
	"strings"
	"workshop-agent/internal/marketplace"
)

// Marketplace commands bypass LLM and memory. Only local authorized reads are exposed here.
func (a *WorkshopAgent) marketplaceMessage(user, workshop int64, text string) (bool, string, error) {
	fields := strings.Fields(text)
	if len(fields) == 0 || fields[0] != "/wb" {
		return false, "", nil
	}
	if a.Marketplace == nil {
		return true, "Модуль WB не настроен.", nil
	}
	kind := "status"
	if len(fields) > 1 {
		kind = fields[1]
	}
	switch kind {
	case "status", "cards", "stocks", "orders":
	default:
		return true, "Управление WB выполняется кнопками /wb в Telegram.", nil
	}
	offset := 0
	var err error
	if len(fields) == 3 {
		offset, err = strconv.Atoi(fields[2])
		if err != nil {
			return true, "", marketplace.ErrInput
		}
	} else if len(fields) > 3 {
		return true, "", marketplace.ErrInput
	}
	answer, err := a.Marketplace.ReadText(marketplace.Scope{UserID: user, WorkshopID: workshop}, kind, offset)
	return true, answer, err
}
