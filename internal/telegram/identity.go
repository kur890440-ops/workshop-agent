package telegram

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"workshop-agent/internal/auth"
	"workshop-agent/internal/invariants"
	"workshop-agent/internal/inventory"
	"workshop-agent/internal/llm"
	"workshop-agent/internal/memory"
	"workshop-agent/internal/products"
	"workshop-agent/internal/workshops"
)

type sessionKey struct{ ChatID, UserID int64 }
type uiState struct {
	Kind    string
	Expires time.Time
}
type buttonAction struct {
	Form        *setupSession
	FormVersion int
	Key         sessionKey
	Action      string
	Workshop    int64
	Target      int64
	Value       string
	Expires     time.Time
}
type telegramCallback struct {
	ID      string           `json:"id"`
	From    telegramUser     `json:"from"`
	Message *telegramMessage `json:"message"`
	Data    string           `json:"data"`
}
type inlineButton struct {
	Text string `json:"text"`
	Data string `json:"callback_data"`
}
type choice struct {
	Text, Action     string
	Workshop, Target int64
	Value            string
}

func publicError(err error) string {
	var transitionDenied *memory.TransitionDenied
	if errors.As(err, &transitionDenied) {
		return transitionDenied.Error()
	}
	if errors.Is(err, errFormExpired) {
		return errFormExpired.Error()
	}
	var denied *invariants.Denied
	if errors.As(err, &denied) {
		return denied.Error()
	}
	for _, e := range []error{invariants.ErrProtected, invariants.ErrConfirmation, invariants.ErrVersion} {
		if errors.Is(err, e) {
			return e.Error()
		}
	}
	var posting *products.PostingError
	if errors.As(err, &posting) {
		return posting.Error()
	}
	if errors.Is(err, products.ErrProductionChanged) {
		return products.ErrProductionChanged.Error()
	}
	if errors.Is(err, memory.ErrPostingRequired) {
		return memory.ErrPostingRequired.Error()
	}
	for _, e := range []error{memory.ErrTransition, memory.ErrTaskChanged, memory.ErrForeground} {
		if errors.Is(err, e) {
			return e.Error()
		}
	}
	for _, e := range []error{products.ErrBOMQuantity, products.ErrBOMDuplicate, products.ErrIncompleteBOM} {
		if errors.Is(err, e) {
			return e.Error()
		}
	}
	if errors.Is(err, errProductEditExpired) {
		return errProductEditExpired.Error()
	}
	if errors.Is(err, products.ErrInvalidEdit) {
		return products.ErrInvalidEdit.Error()
	}
	if errors.Is(err, memory.ErrActiveTask) {
		return memory.ErrActiveTask.Error()
	}
	for _, known := range []error{auth.ErrDenied, auth.ErrDisabled, auth.ErrChooseWorkshop, workshops.ErrInvite, workshops.ErrMemberExists, workshops.ErrOwner, memory.ErrScope, memory.ErrDomain, memory.ErrNoTask} {
		if errors.Is(err, known) {
			return known.Error()
		}
	}
	return "Не удалось выполнить операцию. Проверьте данные и попробуйте ещё раз."
}
func roleName(role auth.Role) string {
	switch role {
	case auth.Owner:
		return "Владелец"
	case auth.Admin:
		return "Администратор"
	case auth.Employee:
		return "Сотрудник"
	case auth.Viewer:
		return "Только просмотр"
	}
	return string(role)
}

func (b *Bot) api(method string, payload any, out any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	resp, err := b.HTTPClient.Post(fmt.Sprintf("https://api.telegram.org/bot%s/%s", b.Token, method), "application/json", bytes.NewReader(raw))
	if err != nil {
		return errors.New("Telegram недоступен")
	}
	defer resp.Body.Close()
	var envelope struct {
		OK          bool            `json:"ok"`
		Result      json.RawMessage `json:"result"`
		Code        int             `json:"error_code"`
		Description string          `json:"description"`
		Parameters  struct {
			RetryAfter int `json:"retry_after"`
		} `json:"parameters"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&envelope); err != nil {
		return err
	}
	if !envelope.OK || resp.StatusCode != http.StatusOK {
		code := envelope.Code
		if code == 0 {
			code = resp.StatusCode
		}
		return &telegramAPIError{Code: code, RetryAfter: envelope.Parameters.RetryAfter, Missing: code == 400 && strings.Contains(strings.ToLower(envelope.Description), "message to delete not found")}
	}
	if method == "sendMessage" {
		var msg telegramMessage
		if err := json.Unmarshal(envelope.Result, &msg); err != nil {
			return errors.New("Telegram returned an invalid message")
		}
		if msg.Chat.Type == "private" && msg.MessageID > 0 {
			var user int64
			if err := b.WS.DB().QueryRow(`SELECT id FROM users WHERE telegram_user_id=?`, msg.Chat.ID).Scan(&user); err != nil {
				return errors.New("Cannot track outgoing Telegram message")
			}
			if err := b.trackMessage(&msg, user); err != nil {
				return errors.New("Cannot track outgoing Telegram message")
			}
		}
	}
	if out != nil {
		return json.Unmarshal(envelope.Result, out)
	}
	return nil
}
func (b *Bot) screen(key sessionKey, text string, choices ...choice) error {
	s := b.getSetup(key.ChatID, key.UserID)
	if s != nil {
		choices = b.formChoices(key, s, text, choices)
	} else {
		choices = b.dialogChoices(key, choices)
	}
	rows := [][]inlineButton{}
	b.uiMu.Lock()
	if b.buttons == nil {
		b.buttons = map[string]buttonAction{}
	}
	if b.ui == nil {
		b.ui = map[sessionKey]*uiState{}
	}
	for id, action := range b.buttons {
		if time.Now().After(action.Expires) {
			delete(b.buttons, id)
		}
	}
	for k, state := range b.ui {
		if time.Now().After(state.Expires) {
			delete(b.ui, k)
		}
	}
	for _, c := range choices {
		token := make([]byte, 16)
		if _, err := rand.Read(token); err != nil {
			b.uiMu.Unlock()
			return err
		}
		id := base64.RawURLEncoding.EncodeToString(token)
		a := buttonAction{Key: key, Action: c.Action, Workshop: c.Workshop, Target: c.Target, Value: c.Value, Expires: time.Now().Add(15 * time.Minute), Form: s}
		if s != nil {
			a.FormVersion = s.navigation.Version
		}
		b.buttons[id] = a
		rows = append(rows, []inlineButton{{Text: c.Text, Data: "wa:" + id}})
	}
	b.uiMu.Unlock()
	var msg telegramMessage
	err := b.api("sendMessage", map[string]any{"chat_id": key.ChatID, "text": text, "reply_markup": map[string]any{"inline_keyboard": rows}, "disable_web_page_preview": true}, &msg)
	if s != nil && s.navigation != nil && err == nil {
		s.navigation.MessageID = msg.MessageID
	}
	return err
}
func (b *Bot) handleIdentityMessage(msg *telegramMessage, user int64, text string) (bool, error) {
	key := sessionKey{msg.Chat.ID, user}
	fields := strings.Fields(text)
	command := strings.Split(fields[0], "@")[0]
	if command == "/start" && len(fields) > 1 && strings.HasPrefix(fields[1], "invite_") {
		if msg.Chat.Type != "private" {
			return true, b.sendMessage(key.ChatID, "Откройте приглашение в личном чате с ботом.")
		}
		token := strings.TrimPrefix(fields[1], "invite_")
		inv, err := b.WS.PreviewInvite(token)
		if err != nil {
			return true, b.sendMessage(key.ChatID, publicError(err))
		}
		b.clearSetup(key.ChatID, key.UserID)
		return true, b.screen(key, fmt.Sprintf("Вас приглашают в мастерскую:\n%s\n\nРоль: %s", inv.WorkshopName, roleName(inv.Role)), choice{Text: "Присоединиться", Action: "accept", Value: token}, choice{Text: "Отмена", Action: "home"})
	}
	if command == "/start" || command == "/workshop" || text == "⚙️ Мастерская" {
		b.clearSetup(key.ChatID, key.UserID)
		b.uiMu.Lock()
		delete(b.ui, key)
		b.uiMu.Unlock()
		return true, b.home(key)
	}
	if command == "/workshops" {
		return true, b.switcher(key)
	}
	if command == "/members" {
		workshop, err := b.WS.ActiveWorkshop(user)
		if err != nil {
			return true, b.home(key)
		}
		return true, b.membersScreen(key, workshop, 0)
	}
	b.uiMu.Lock()
	state := b.ui[key]
	if state != nil && time.Now().After(state.Expires) {
		delete(b.ui, key)
		state = nil
	}
	b.uiMu.Unlock()
	if state != nil && state.Kind == "create" {
		if command == "/cancel" {
			b.uiMu.Lock()
			delete(b.ui, key)
			b.uiMu.Unlock()
			return true, b.home(key)
		}
		if strings.HasPrefix(text, "/") {
			return true, b.sendMessage(key.ChatID, "Введите название или /cancel.")
		}
		if len([]rune(text)) > 120 {
			return true, b.sendMessage(key.ChatID, "Название должно быть не длиннее 120 символов.")
		}
		b.uiMu.Lock()
		b.ui[key] = &uiState{"create_confirm", time.Now().Add(15 * time.Minute)}
		b.uiMu.Unlock()
		return true, b.screen(key, "Создать мастерскую «"+text+"»?", choice{Text: "Создать", Action: "create_confirm", Value: text}, choice{Text: "Отмена", Action: "home"})
	}
	return false, nil
}
func (b *Bot) home(key sessionKey) error {
	available, err := b.WS.AvailableWorkshops(key.UserID)
	if err != nil {
		return err
	}
	active, err := b.WS.ActiveWorkshop(key.UserID)
	if err != nil && !errors.Is(err, auth.ErrChooseWorkshop) {
		return err
	}
	if len(available) == 0 {
		return b.screen(key, "У вас пока нет доступной мастерской. Если доступ отключён, обратитесь к её администратору.", choice{Text: "Создать мастерскую", Action: "create"})
	}
	if active == 0 {
		return b.switcher(key)
	}
	var current workshops.WorkshopSummary
	for _, w := range available {
		if w.ID == active {
			current = w
		}
	}
	choices := []choice{{Text: "👥 Сотрудники", Action: "members", Workshop: active}, {Text: "🔄 Сменить мастерскую", Action: "switch"}, {Text: "Создать мастерскую", Action: "create"}, {Text: "Покинуть мастерскую", Action: "confirm_left", Workshop: active, Target: key.UserID}}
	if auth.Has(current.Role, auth.MembersInvite) {
		// Profile remains a user-scoped screen, independent of membership management.
		choices = append(choices, choice{Text: "➕ Пригласить", Action: "invite_roles", Workshop: active}, choice{Text: "Приглашения", Action: "invites", Workshop: active})
	}
	choices = append(choices, choice{Text: "👤 Профиль", Action: "profile_home"})
	choices = append(choices, choice{Text: "📋 Текущая задача", Action: "fsm_show", Workshop: active})
	choices = append(choices, choice{Text: "Задачи", Action: "orders_all", Workshop: active})
	choices = append(choices, choice{Text: "📊 Сводка за сегодня", Action: "daily_summary", Workshop: active})
	return b.screen(key, "⚙️ Мастерская\n🏭 Текущая мастерская: "+current.Name+"\nВаша роль: "+roleName(current.Role)+"\n\nСклад: /materials, /products, /stock, /to_order\nНастройка: /setup", choices...)
}
func (b *Bot) switcher(key sessionKey) error {
	list, err := b.WS.AvailableWorkshops(key.UserID)
	if err != nil {
		return err
	}
	var current int64
	_ = b.WS.DB().QueryRow(`SELECT active_workshop_id FROM user_workshop_context WHERE user_id=?`, key.UserID).Scan(&current)
	choices := []choice{}
	for _, w := range list {
		name := w.Name
		if current == w.ID {
			name = "✓ " + name
		}
		choices = append(choices, choice{Text: name, Action: "switch_to", Workshop: w.ID})
	}
	choices = append(choices, choice{Text: "Создать мастерскую", Action: "create"})
	return b.screen(key, "Выберите мастерскую:", choices...)
}
func (b *Bot) membersScreen(key sessionKey, workshop, selected int64) error {
	members, err := b.WS.Members(key.UserID, workshop)
	if err != nil {
		return err
	}
	canManage := auth.Require(b.WS.DB(), key.UserID, workshop, auth.MembersManage) == nil
	choices := []choice{}
	if selected != 0 {
		for _, m := range members {
			if m.UserID != selected {
				continue
			}
			if canManage && m.Role != auth.Owner {
				choices = append(choices, choice{Text: "Изменить роль", Action: "role_choices", Workshop: workshop, Target: selected})
				if m.Status == "active" {
					choices = append(choices, choice{Text: "Отключить доступ", Action: "confirm_disabled", Workshop: workshop, Target: selected})
				} else {
					choices = append(choices, choice{Text: "Восстановить доступ", Action: "confirm_active", Workshop: workshop, Target: selected})
				}
				choices = append(choices, choice{Text: "Удалить из мастерской", Action: "confirm_removed", Workshop: workshop, Target: selected})
			}
			if selected != key.UserID && m.Status == "active" && auth.Require(b.WS.DB(), key.UserID, workshop, auth.OwnershipTransfer) == nil {
				choices = append(choices, choice{Text: "Передать владение", Action: "confirm_transfer", Workshop: workshop, Target: selected})
			}
			choices = append(choices, choice{Text: "Назад", Action: "members", Workshop: workshop})
			username := m.Username
			if username != "" {
				username = "@" + username
			}
			return b.screen(key, fmt.Sprintf("%s\n%s\nРоль: %s\nСтатус: %s\nДата подключения: %s", m.Name, username, roleName(m.Role), m.Status, m.JoinedAt), choices...)
		}
		return auth.ErrDenied
	}
	for _, m := range members {
		choices = append(choices, choice{Text: fmt.Sprintf("%s — %s (%s)", m.Name, roleName(m.Role), m.Status), Action: "member", Workshop: workshop, Target: m.UserID})
	}
	if auth.Require(b.WS.DB(), key.UserID, workshop, auth.MembersInvite) == nil {
		choices = append(choices, choice{Text: "➕ Пригласить", Action: "invite_roles", Workshop: workshop})
	}
	if canManage {
		choices = append(choices, choice{Text: "⚙️ Управление — выберите сотрудника", Action: "members", Workshop: workshop})
	}
	choices = append(choices, choice{Text: "⚙️ Мастерская", Action: "home"})
	return b.screen(key, "👥 Сотрудники — выберите сотрудника:", choices...)
}
func (b *Bot) rolesScreen(key sessionKey, workshop, target int64, invite bool) error {
	permission := auth.MembersManage
	action := "confirm_role"
	if invite {
		permission = auth.MembersInvite
		action = "confirm_invite"
	}
	if err := auth.Require(b.WS.DB(), key.UserID, workshop, permission); err != nil {
		return err
	}
	choices := []choice{}
	for _, role := range []auth.Role{auth.Admin, auth.Employee, auth.Viewer} {
		choices = append(choices, choice{Text: roleName(role), Action: action, Workshop: workshop, Target: target, Value: string(role)})
	}
	return b.screen(key, "Выберите роль:", choices...)
}
func (b *Bot) invitesScreen(key sessionKey, workshop int64) error {
	list, err := b.WS.Invites(key.UserID, workshop)
	if err != nil {
		return err
	}
	choices := []choice{}
	lines := []string{"Приглашения:"}
	for _, i := range list {
		lines = append(lines, fmt.Sprintf("#%d — %s, %s, использований %d/%d", i.ID, roleName(i.Role), i.Status, i.UsedCount, i.MaxUses))
		if i.Status == "active" {
			choices = append(choices, choice{Text: fmt.Sprintf("Отозвать #%d", i.ID), Action: "confirm_revoke", Workshop: workshop, Target: i.ID})
		}
	}
	choices = append(choices, choice{Text: "Назад", Action: "home"})
	return b.screen(key, strings.Join(lines, "\n"), choices...)
}
func (b *Bot) handleCallback(c *telegramCallback) error {
	if c == nil || c.Message == nil {
		return nil
	}
	_ = b.api("answerCallbackQuery", map[string]any{"callback_query_id": c.ID}, nil)
	user, err := b.WS.UpsertUser(c.From.ID, c.From.Username, c.From.FirstName, c.From.LastName)
	if err != nil {
		return b.sendMessage(c.Message.Chat.ID, publicError(err))
	}
	key := sessionKey{c.Message.Chat.ID, user}
	if c.Message.Chat.Type == "private" {
		if err := b.ownChat(key); err != nil {
			return err
		}
		if err := b.trackMessage(c.Message, user); err != nil {
			return err
		}
	}
	id := strings.TrimPrefix(c.Data, "wa:")
	b.uiMu.Lock()
	action, ok := b.buttons[id]
	if ok && action.Key == key && action.Action != "completion_post" {
		delete(b.buttons, id)
	}
	b.uiMu.Unlock()
	if !ok || action.Key != key || time.Now().After(action.Expires) {
		return b.sendMessage(key.ChatID, "Кнопка устарела или принадлежит другому пользователю. Откройте /workshop заново.")
	}
	// Management links may reveal invitations; use private chats for these screens.
	if c.Message.Chat.Type != "private" {
		return b.sendMessage(key.ChatID, "Управление мастерской доступно в личном чате с ботом: /workshop.")
	}
	err = b.executeButton(action)
	if err != nil {
		return b.sendMessage(key.ChatID, publicError(err))
	}
	return nil
}
func (b *Bot) executeButton(a buttonAction) error {
	key := a.Key
	if a.Form != nil {
		s := b.getSetup(key.ChatID, key.UserID)
		if s != a.Form || s.navigation == nil || s.navigation.Version != a.FormVersion {
			return errFormExpired
		}
	}
	if strings.HasPrefix(a.Action, "form_") {
		return b.formButton(a)
	}
	if strings.HasPrefix(a.Action, "dialog_") {
		return b.dialogButton(a)
	}
	if a.Action == "daily_summary" {
		return b.executeSemantic(key, a.Workshop, &llm.StructuredCommand{Action: "get_daily_summary"}, nil, "local", "/summary")
	}
	if strings.HasPrefix(a.Action, "profile_") {
		return b.profileButton(a)
	}
	if a.Workshop > 0 {
		if err := auth.Require(b.WS.DB(), key.UserID, a.Workshop, auth.WorkshopRead); err != nil {
			return err
		}
		// Old menus cannot mutate a different workshop after the user switches.
		if a.Action != "switch_to" {
			active, err := b.WS.ActiveWorkshop(key.UserID)
			if err != nil {
				return err
			}
			if active != a.Workshop {
				return errors.New("workshop context changed")
			}
		}
	}
	if strings.HasPrefix(a.Action, "orders_") {
		return b.ordersButton(a)
	}
	if a.Action == "invariant_update" {
		return b.invariantButton(a)
	}
	if strings.HasPrefix(a.Action, "completion_") {
		return b.completionButton(a)
	}
	if strings.HasPrefix(a.Action, "assembly_") {
		return b.assemblyButton(a)
	}
	if strings.HasPrefix(a.Action, "product_edit_") {
		return b.productEditButton(a)
	}
	if a.Action == "product_add" {
		b.clearSetup(key.ChatID, key.UserID)
		if err := auth.Require(b.WS.DB(), key.UserID, a.Workshop, auth.ProductsWrite); err != nil {
			return err
		}
		b.startSetup(key.ChatID, &setupSession{kind: "product", userID: key.UserID, workshopID: a.Workshop})
		return b.sendMessage(key.ChatID, "Добавление продукта начато. Введите название или /setup_stop для отмены.")
	}
	if a.Action == "fsm_apply" {
		return b.taskButton(a)
	}
	if a.Action == "fsm_show" {
		handled, err := b.taskMessage(key, "/task")
		if err != nil || handled {
			return err
		}
		return b.sendMessage(key.ChatID, "Новая задача: /task new. Существующая задача: /task.")
	}
	if strings.HasPrefix(a.Action, "confirm_") {
		action := strings.TrimPrefix(a.Action, "confirm_")
		text := "Подтвердить действие?"
		switch action {
		case "transfer":
			text = "Передать владение выбранному сотруднику? Вы станете администратором."
		case "role":
			text = "Назначить роль «" + roleName(auth.Role(a.Value)) + "»?"
		case "invite":
			text = "Создать приглашение с ролью «" + roleName(auth.Role(a.Value)) + "»?"
		case "disabled":
			text = "Отключить доступ сотрудника?"
		case "active":
			text = "Восстановить доступ сотрудника?"
		case "removed":
			text = "Удалить сотрудника из мастерской? Его пользователь и история сохранятся."
		case "left":
			text = "Покинуть мастерскую?"
		case "revoke":
			text = "Отозвать приглашение?"
		}
		return b.screen(key, text, choice{Text: "Подтвердить", Action: action, Workshop: a.Workshop, Target: a.Target, Value: a.Value}, choice{Text: "Отмена", Action: "home"})
	}
	switch a.Action {
	case "semantic_cancel":
		b.invalidateSemantic(key)
		return b.sendMessage(key.ChatID, "Изменение отменено. Остаток не изменён.")
	case "semantic_stock_confirm":
		b.invalidateSemantic(key)
		var p semanticChange
		if err := json.Unmarshal([]byte(a.Value), &p); err != nil {
			return err
		}
		var sessionID int64
		if err := b.WS.DB().QueryRow(`SELECT id FROM conversation_sessions WHERE user_id=? AND workshop_id=? AND telegram_chat_id=? AND status='active'`, key.UserID, a.Workshop, key.ChatID).Scan(&sessionID); err != nil || sessionID != p.SessionID {
			return b.sendMessage(key.ChatID, "Диалог изменился. Повторите запрос изменения остатка.")
		}
		inv := b.Inv.ForUser(key.UserID)
		item, err := inv.Material(a.Workshop, a.Target)
		if err != nil {
			return err
		}
		if inventory.DisplayUnit(item) != p.DisplayUnit {
			return b.sendMessage(key.ChatID, "Рабочая единица изменилась. Повторите запрос.")
		}
		base := inventory.ConvertToBase(p.Unit, p.Amount)
		if p.Mode == "decrease" {
			base = -base
		}
		stock, _, err := inv.ChangeDisplayedStock(a.Workshop, a.Target, inventory.ConvertFromBase(p.DisplayUnit, base), p.Mode == "absolute", p.DisplayUnit)
		if err != nil {
			return err
		}
		b.selectMaterial(key, a.Workshop, a.Target)
		return b.sendMessage(key.ChatID, fmt.Sprintf("%s — %s. Изменение сохранено.", item["name"], inventory.FormatQuantity(stock, p.DisplayUnit)))
	case "clear_execute":
		return b.clearChat(key)
	case "clear_cancel":
		if err := b.ownChat(key); err != nil {
			return err
		}
		b.invalidateClear(key, false)
		return b.sendMessage(key.ChatID, "Очистка отменена. Сообщения и контекст сохранены.")
	case "materials":
		return b.materialsMenu(key, a.Workshop)
	case "material_unit_confirm":
		if err := auth.Require(b.WS.DB(), key.UserID, a.Workshop, auth.InventoryWrite); err != nil {
			return err
		}
		if s := b.getSetup(key.ChatID, key.UserID); s != nil && s.kind == "material_unit" {
			s.stage = 2
		}
		return b.screen(key, "Установить рабочую единицу «"+materialUnit(a.Value)+"»? Количество на складе не изменится.", choice{Text: "Подтвердить", Action: "material_unit_save", Workshop: a.Workshop, Target: a.Target, Value: a.Value}, choice{Text: "Отмена", Action: "materials", Workshop: a.Workshop})
	case "material_unit_save":
		if err := b.Inv.ForUser(key.UserID).SetDisplayUnit(a.Workshop, a.Target, a.Value); err != nil {
			return err
		}
		return b.materialsMenu(key, a.Workshop)
	case "materials_add", "materials_edit", "materials_unit":
		if err := auth.Require(b.WS.DB(), key.UserID, a.Workshop, auth.InventoryWrite); err != nil {
			return err
		}
		s := &setupSession{kind: "material", workshopID: a.Workshop, userID: key.UserID}
		if a.Action == "materials_edit" || a.Action == "materials_unit" {
			s.kind = "material_stock"
			if a.Action == "materials_unit" {
				s.kind = "material_unit"
			}
			if err := json.Unmarshal([]byte(a.Value), &s.materialIDs); err != nil {
				return err
			}
		}
		b.startSetup(key.ChatID, s)
		if s.kind == "material_stock" || s.kind == "material_unit" {
			return b.sendMessage(key.ChatID, "Введите номер материала из списка. Отмена: /setup_stop")
		}
		return b.sendMessage(key.ChatID, "Введите название нового материала. Отмена: /setup_stop")
	case "edit_material", "edit_product":
		var payload map[string]string
		if err := json.Unmarshal([]byte(a.Value), &payload); err != nil {
			return err
		}
		var err error
		if a.Action == "edit_material" {
			inv := b.Inv.ForUser(key.UserID)
			if payload["field"] == "current_stock" || payload["field"] == "minimum_stock" {
				var value float64
				value, err = strconv.ParseFloat(strings.ReplaceAll(payload["value"], ",", "."), 64)
				if err == nil {
					if payload["field"] == "current_stock" {
						_, _, err = inv.ChangeDisplayedStock(a.Workshop, a.Target, value, true, payload["unit"])
					} else {
						err = inv.SetDisplayedMinimum(a.Workshop, a.Target, value, payload["unit"])
					}
				}
			} else {
				err = inv.UpdateMaterial(a.Workshop, a.Target, payload["field"], payload["value"], key.UserID, "")
			}
		} else {
			err = b.Prod.ForUser(key.UserID).UpdateProduct(a.Workshop, a.Target, payload["field"], payload["value"], key.UserID, "")
		}
		if err != nil {
			return err
		}
		return b.sendMessage(key.ChatID, "Изменения сохранены.")
	case "home":
		if s := b.getSetup(key.ChatID, key.UserID); s != nil {
			return b.formExit(key, s, "home")
		}
		return b.home(key)
	case "switch":
		if s := b.getSetup(key.ChatID, key.UserID); s != nil {
			return b.formExit(key, s, "switch")
		}
		return b.switcher(key)
	case "switch_to":
		if err := b.dropAssembly(key); err != nil {
			return err
		}
		if err := b.WS.SetActiveWorkshop(key.UserID, a.Workshop); err != nil {
			return err
		}
		b.clearSetup(key.ChatID, key.UserID)
		b.forgetMaterialList(key)
		return b.home(key)
	case "create":
		b.clearSetup(key.ChatID, key.UserID)
		b.uiMu.Lock()
		if b.ui == nil {
			b.ui = map[sessionKey]*uiState{}
		}
		b.ui[key] = &uiState{"create", time.Now().Add(15 * time.Minute)}
		b.uiMu.Unlock()
		return b.sendMessage(key.ChatID, "Введите название мастерской или /cancel.")
	case "create_confirm":
		b.uiMu.Lock()
		state := b.ui[key]
		delete(b.ui, key)
		b.uiMu.Unlock()
		if state == nil || state.Kind != "create_confirm" || time.Now().After(state.Expires) {
			return errFormExpired
		}
		if err := b.dropAssembly(key); err != nil {
			return err
		}
		if _, err := b.WS.CreateOwnedWorkshop(key.UserID, a.Value); err != nil {
			return err
		}
		return b.home(key)
	case "accept":
		if err := b.dropAssembly(key); err != nil {
			return err
		}
		if _, err := b.WS.AcceptInvite(key.UserID, a.Value); err != nil {
			return err
		}
		b.clearSetup(key.ChatID, key.UserID)
		return b.home(key)
	case "members":
		return b.membersScreen(key, a.Workshop, 0)
	case "member":
		return b.membersScreen(key, a.Workshop, a.Target)
	case "invite_roles":
		return b.rolesScreen(key, a.Workshop, 0, true)
	case "role_choices":
		return b.rolesScreen(key, a.Workshop, a.Target, false)
	case "invites":
		return b.invitesScreen(key, a.Workshop)
	case "invite":
		if b.BotUsername == "" {
			var me struct {
				Username string `json:"username"`
			}
			if err := b.api("getMe", map[string]any{}, &me); err != nil {
				return err
			}
			b.BotUsername = me.Username
		}
		if b.BotUsername == "" {
			return errors.New("missing bot username")
		}
		i, token, err := b.WS.CreateInvite(key.UserID, a.Workshop, auth.Role(a.Value), 0, 0)
		if err != nil {
			return err
		}
		return b.screen(key, fmt.Sprintf("Приглашение #%d\nРоль: %s\nДействует до: %s\nИспользований: %d\n\nhttps://t.me/%s?start=invite_%s\n\nПерешлите ссылку сотруднику.", i.ID, roleName(i.Role), time.Unix(i.ExpiresAt, 0).UTC().Format(time.RFC3339), i.MaxUses, b.BotUsername, token), choice{Text: "Приглашения", Action: "invites", Workshop: a.Workshop})
	case "role":
		if err := b.WS.ChangeRole(key.UserID, a.Workshop, a.Target, auth.Role(a.Value)); err != nil {
			return err
		}
		return b.membersScreen(key, a.Workshop, a.Target)
	case "disabled", "active", "removed", "left":
		if err := b.WS.ChangeMemberStatus(key.UserID, a.Workshop, a.Target, a.Action); err != nil {
			return err
		}
		return b.home(key)
	case "transfer":
		if err := b.WS.TransferOwnership(a.Workshop, key.UserID, a.Target); err != nil {
			return err
		}
		return b.home(key)
	case "revoke":
		if err := b.WS.RevokeInvite(key.UserID, a.Workshop, a.Target); err != nil {
			return err
		}
		return b.invitesScreen(key, a.Workshop)
	}
	return auth.ErrDenied
}

// ParseInternalID is shared by deterministic CLI diagnostics.
func ParseInternalID(value string) (int64, error) {
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.New("ID должен быть положительным числом")
	}
	return id, nil
}
