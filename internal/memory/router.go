package memory

import (
	"regexp"
	"strconv"
	"strings"
)

var number = regexp.MustCompile(`\d+(?:[.,]\d+)?`)
var unitFact = regexp.MustCompile(`(?i)\d+(?:[.,]\d+)?\s*(?:кг|kg|грам|литр|штук|шт\b|pcs|кист|короб)`)

func IsDomainFact(text string) bool {
	s := strings.ToLower(text)
	return containsAny(s, "на складе", "остаток", "остатки", "inventory", "bom", "спецификац", "сегодня произвел", "сегодня произвёл", "сегодня произвели") || unitFact.MatchString(s)
}

type MemoryRouter struct{}

func (MemoryRouter) Route(text string, scope Scope, task *Task) Candidate {
	s := strings.ToLower(strings.TrimSpace(text))
	c := Candidate{Content: text, Scope: scope, Target: Short, Category: "dialog", Confidence: 0.8, Reason: "Сообщение относится к текущему диалогу; долговременная запись не запрошена."}
	if containsAny(s, "api_key", "api key", "пароль:", "token=", "invite_") {
		c.Target = Discard
		c.Reason = "Секрет или приглашение не сохраняется в память."
		return c
	}
	temporary := containsAny(s, "для этой партии", "для этого заказа", "только сейчас", "только для этой", "на эту партию")
	question := strings.Contains(s, "?") || strings.HasPrefix(s, "какой") || strings.HasPrefix(s, "какую") || strings.HasPrefix(s, "что ")
	if temporary && !question {
		c.Target = Working
		c.Category = "task_parameter"
		c.Key = "override"
		c.Value = text
		c.Reason = "Параметр ограничен текущей задачей; постоянные правила не меняются."
		if containsAny(s, "box", "короб", "упаков") {
			c.Key = "packaging"
			if idx := strings.Index(s, "box "); idx >= 0 {
				c.Value = strings.TrimSpace(text[idx:])
			}
		}
		if containsAny(s, "petg", "материал", "пластик") {
			c.Key = "material"
		}
		return c
	}
	if containsAny(s, "всегда", "теперь", "с этого момента", "bom", "спецификац") && containsAny(s, "кист", "комплект", "набор", "короб", "упаков") {
		c.Target = Domain
		c.Category = "bom_update"
		c.RequiresConfirmation = true
		c.Reason = "Состав изделия — источник истины BOM; изменение выполняется доменным сервисом."
		if n := number.FindString(s); n != "" {
			c.Quantity, _ = strconv.ParseFloat(strings.ReplaceAll(n, ",", "."), 64)
		}
		return c
	}
	if containsAny(s, "соберем", "соберём", "запланируй", "партия", "рассчитай партию") && number.MatchString(s) {
		c.Target = Working
		c.Category = "assembly"
		c.Key = "create"
		c.Quantity, _ = strconv.ParseFloat(strings.ReplaceAll(number.FindString(s), ",", "."), 64)
		c.Reason = "Начало рабочей задачи; количество хранится в task state."
		return c
	}
	if task != nil && containsAny(s, "нет,", "сделай", "сделать", "количество", "теперь ") && number.MatchString(s) {
		c.Target = Working
		c.Category = task.Type
		c.Key = "quantity"
		c.Quantity, _ = strconv.ParseFloat(strings.ReplaceAll(number.FindString(s), ",", "."), 64)
		c.Reason = "Уточнение количества активной задачи."
		return c
	}
	if IsDomainFact(s) {
		c.Target = Domain
		c.Category = "domain_fact"
		c.Reason = "Фактические остатки, состав и производство относятся к Domain DB, не к долговременной памяти."
		return c
	}
	explicit := containsAny(s, "запомни", "всегда", "с этого момента") || strings.HasPrefix(s, "отвечай ")
	if explicit && containsAny(s, "для меня", "мне", "я предпочитаю", "отвечай", "итог", "отчет", "отчёт") && !containsAny(s, "для этой мастерской", "в нашей мастерской") {
		c.Target = Personal
		c.Category = "USER_PREFERENCE"
		c.RequiresConfirmation = true
		c.Reason = "Явно запрошено устойчивое личное предпочтение пользователя."
		switch {
		case containsAny(s, "итог", "сначала"):
			c.Key = "summary_first"
			c.Value = "true"
		case containsAny(s, "кратк", "коротк"):
			c.Key = "response_style"
			c.Value = "concise"
		case containsAny(s, "таблиц"):
			c.Key = "response_format"
			c.Value = "table"
		default:
			c.Category = "USER_PROFILE"
			c.Key = "profile"
			c.Value = text
		}
		return c
	}
	if explicit && containsAny(s, "мастерской", "процесс", "контрол", "проверя") {
		c.Target = Workshop
		c.Category = "PROCESS_RULE"
		c.Key = "quality_control"
		c.Value = text
		c.RequiresConfirmation = true
		c.Reason = "Общее правило процесса мастерской; запись требует workshop.settings.manage."
		return c
	}
	return c
}
