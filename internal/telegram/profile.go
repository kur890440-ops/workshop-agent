package telegram

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"workshop-agent/internal/agent"
	"workshop-agent/internal/personalization"
)

func (b *Bot) profileMessage(key sessionKey, text string) (bool, error) {
	if strings.EqualFold(text, "/profile trace") {
		r, err := personalization.New(b.WS.DB()).ForUser(key.UserID).LastTrace()
		if errors.Is(err, sql.ErrNoRows) {
			return true, b.sendMessage(key.ChatID, "Пока нет запроса с применённым профилем.")
		}
		if err != nil {
			return true, err
		}
		raw, _ := json.Marshal(r.Overrides)
		text := fmt.Sprintf("USER PROFILE\nstyle=%s; detail_level=%s; format=%s; summary_first=%t\nCURRENT OVERRIDE\n%s\nAPPLIED\nstyle=%s; detail_level=%s; format=%s; summary_first=%t; language=%s\nProfile context: ~%d tokens (оценка).\nPermissions и обязательные подтверждения сохраняются.", r.Stored.Style, r.Stored.Detail, r.Stored.Format, r.Stored.SummaryFirst, string(raw), r.Applied.Style, r.Applied.Detail, r.Applied.Format, r.Applied.SummaryFirst, r.Applied.Language, r.EstimatedTokens)
		return true, b.sendMessage(key.ChatID, text)
	}
	if strings.EqualFold(text, "/profile") {
		return true, b.profileScreen(key)
	}
	if b.getSetup(key.ChatID, key.UserID) != nil {
		return false, nil
	}
	if strings.EqualFold(text, "профиль") || text == "👤 Профиль" {
		return true, b.profileScreen(key)
	}
	if k, v, ok := personalization.PersistentIntent(text); ok {
		if err := personalization.New(b.WS.DB()).ForUser(key.UserID).UpdatePreference(k, v, "explicit_telegram_message"); err != nil {
			return true, err
		}
		return true, b.profileScreen(key)
	}
	if b.Agent != nil && (agent.IsDailySummary(text) || agent.IsPersonalizedReport(text)) {
		w, err := b.WS.ActiveWorkshop(key.UserID)
		if err != nil {
			return true, err
		}
		answer, usage, err := b.Agent.HandleMessageForWorkshop(context.Background(), w, key.UserID, key.ChatID, text)
		if err != nil {
			return true, err
		}
		return true, b.sendMessage(key.ChatID, answer+"\n\n"+formatTokenUsage(usage))
	}
	return false, nil
}

var profileLabels = map[string]string{"style": "Стиль", "response_style": "Подробность", "response_format": "Формат", "summary_first": "Итог сначала", "confirmation_level": "Подтверждения", "hide_llm_details": "Ограничения"}
var valueLabels = map[string]string{"neutral": "Нейтральный", "concise": "Кратко", "friendly": "Дружелюбный", "technical": "Технический", "formal": "Формальный", "normal": "Обычно", "detailed": "Подробно", "brief": "Кратко", "text": "Обычный текст", "list": "Список", "table": "Таблица", "true": "Да", "false": "Нет", "always_confirm": "Всегда подтверждать", "confirm_destructive": "Подтверждать важные операции", "minimal_confirmation": "Минимум подтверждений"}

func (b *Bot) profileScreen(key sessionKey) error {
	p, err := personalization.New(b.WS.DB()).ForUser(key.UserID).GetProfile()
	if err != nil {
		return err
	}
	text := fmt.Sprintf("👤 Мой профиль\nСтиль: %s\nПодробность: %s\nФормат: %s\nИтог сначала: %t\nПодтверждения: %s\nСкрывать технические детали LLM: %t\nЯзык интерфейса: русский\nИзменения BOM и производство всегда требуют подтверждения по бизнес-правилам.", valueLabels[p.Style], valueLabels[p.Detail], valueLabels[p.Format], p.SummaryFirst, valueLabels[p.Confirmation], p.HideLLM)
	b.invalidateProfileButtons(key)
	choices := []choice{}
	for _, k := range []string{"style", "response_style", "response_format", "summary_first", "confirmation_level", "hide_llm_details"} {
		choices = append(choices, choice{Text: profileLabels[k], Action: "profile_field", Value: k})
	}
	choices = append(choices, choice{Text: "Сбросить профиль", Action: "profile_reset_prompt"})
	return b.screen(key, text, choices...)
}
func (b *Bot) invalidateProfileButtons(key sessionKey) {
	b.uiMu.Lock()
	defer b.uiMu.Unlock()
	for id, a := range b.buttons {
		if a.Key == key && strings.HasPrefix(a.Action, "profile_") {
			delete(b.buttons, id)
		}
	}
}
func (b *Bot) profileButton(a buttonAction) error {
	svc := personalization.New(b.WS.DB()).ForUser(a.Key.UserID)
	if _, err := svc.GetProfile(); err != nil {
		return err
	}
	b.invalidateProfileButtons(a.Key)
	switch a.Action {
	case "profile_home":
		return b.profileScreen(a.Key)
	case "profile_field":
		options, ok := personalization.Allowed[a.Value]
		if !ok {
			return errors.New("invalid preference")
		}
		choices := []choice{}
		for _, v := range options {
			choices = append(choices, choice{Text: valueLabels[v], Action: "profile_set", Value: a.Value + "=" + v})
		}
		choices = append(choices, choice{Text: "Назад к профилю", Action: "profile_home"})
		text := profileLabels[a.Value]
		if a.Value == "hide_llm_details" {
			text = "Скрывать технические детали LLM? Единицы и обязательные подтверждения сохраняются."
		}
		return b.screen(a.Key, text, choices...)
	case "profile_set":
		parts := strings.SplitN(a.Value, "=", 2)
		if len(parts) != 2 {
			return errors.New("invalid preference")
		}
		if err := svc.UpdatePreference(parts[0], parts[1], "profile_button"); err != nil {
			return err
		}
		return b.profileScreen(a.Key)
	case "profile_reset_prompt":
		return b.screen(a.Key, "Сбросить личные настройки ответа? Данные мастерских и задачи сохранятся.", choice{Text: "Сбросить", Action: "profile_reset"}, choice{Text: "Отмена", Action: "profile_home"})
	case "profile_reset":
		if err := svc.Reset(); err != nil {
			return err
		}
		return b.profileScreen(a.Key)
	}
	return errors.New("invalid profile action")
}
