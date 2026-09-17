package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"workshop-agent/internal/agent"
	"workshop-agent/internal/audit"
	"workshop-agent/internal/auth"
	"workshop-agent/internal/inventory"
	"workshop-agent/internal/llm"
	"workshop-agent/internal/personalization"
	"workshop-agent/internal/products"
	"workshop-agent/internal/storage"
	"workshop-agent/internal/workshops"
)

type Bot struct {
	materialLists map[sessionKey]materialListContext
	Token         string
	WS            *workshops.Service
	Inv           *inventory.Service
	Prod          *products.Service
	Audit         *audit.Service
	Started       bool
	HTTPClient    *http.Client
	Agent         *agent.WorkshopAgent
	setupMu       sync.Mutex
	setup         map[sessionKey]*setupSession
	uiMu          sync.Mutex
	ui            map[sessionKey]*uiState
	buttons       map[string]buttonAction
	BotUsername   string
}

type setupSession struct {
	composition *compositionInput
	productID   int64
	productIDs  []int64
	edit        products.Edit
	editExpires time.Time
	editSession int64
	materialIDs []int64
	materialID  int64
	kind        string
	stage       int
	workshopID  int64
	userID      int64
	actorName   string
	name        string
	category    string
	unit        string
	stock       float64
	minimum     float64
	sku         string
	productType string
}

func NewBot(token string, ws *workshops.Service, inv *inventory.Service, prod *products.Service, auditSvc *audit.Service, agentSvc *agent.WorkshopAgent) (*Bot, error) {
	if token == "" {
		return nil, logError("TELEGRAM_BOT_TOKEN is empty")
	}
	return &Bot{Token: token, WS: ws, Inv: inv, Prod: prod, Audit: auditSvc, Agent: agentSvc, Started: true, HTTPClient: &http.Client{Timeout: 15 * time.Second}, setup: make(map[sessionKey]*setupSession)}, nil
}

func logError(msg string) error {
	return &botError{msg: msg}
}

type botError struct{ msg string }

func (e *botError) Error() string { return e.msg }

func (b *Bot) Start() {
	if !b.Started {
		log.Println("Telegram-бот не инициализирован.")
		return
	}
	log.Println("Telegram-бот запущен. Начинаю long-polling обновлений.")
	offset := int64(0)
	for {
		updates, err := b.getUpdates(offset)
		if err != nil {
			log.Printf("polling error: %v", err)
			time.Sleep(5 * time.Second)
			continue
		}
		for _, update := range updates {
			if update.UpdateID >= offset {
				offset = update.UpdateID + 1
			}
			if update.CallbackQuery != nil {
				if err := b.handleCallback(update.CallbackQuery); err != nil {
					log.Printf("callback failed")
				}
				continue
			}
			if update.Message == nil {
				continue
			}
			if err := b.handleMessage(update.Message); err != nil {
				log.Printf("handle message error")
			}
		}
		if len(updates) == 0 {
			time.Sleep(1 * time.Second)
		}
	}
}

func (b *Bot) getUpdates(offset int64) ([]telegramUpdate, error) {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/getUpdates?offset=%d&timeout=30&allowed_updates=%s", b.Token, offset, urlQueryEscape("[\"message\",\"callback_query\"]"))
	// Long polling must outlive Telegram's 30-second wait. Keep ordinary API
	// requests on the original short timeout and retain the configured transport.
	pollClient := *b.HTTPClient
	pollClient.Timeout = 45 * time.Second
	resp, err := pollClient.Get(url)
	if err != nil {
		var networkError net.Error
		if errors.As(err, &networkError) && networkError.Timeout() {
			return nil, errors.New("Telegram getUpdates timed out after 45s")
		}
		return nil, errors.New("Telegram getUpdates connection failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("telegram getUpdates failed: status=%s", resp.Status)
	}
	var payload telegramResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	if !payload.OK {
		return nil, errors.New("telegram API returned ok=false")
	}
	return payload.Result, nil
}

func (b *Bot) processMessage(msg *telegramMessage) error {
	if msg == nil {
		return nil
	}
	if msg.Chat.Type != "private" {
		return b.sendMessage(msg.Chat.ID, "Работа с мастерской доступна в личном чате с ботом. Откройте /start там.")
	}
	if msg.Chat.ID != msg.From.ID {
		return auth.ErrDenied
	}
	text := strings.TrimSpace(msg.Text)
	chatID := msg.Chat.ID
	userID, err := b.WS.UpsertUser(msg.From.ID, msg.From.Username, msg.From.FirstName, msg.From.LastName)
	if err != nil {
		return b.sendMessage(chatID, publicError(err))
	}
	if err := b.trackMessage(msg, userID); err != nil {
		return err
	}
	if text == "" {
		return nil
	}
	if isClearCommand(text) {
		return b.clearPrompt(sessionKey{chatID, userID})
	}
	if handled, err := b.profileMessage(sessionKey{chatID, userID}, text); handled {
		return err
	}
	if handled, err := b.handleIdentityMessage(msg, userID, text); handled {
		return err
	}
	if b.getSetup(chatID, userID) == nil && (strings.EqualFold(text, "задачи") || strings.EqualFold(text, "задача")) {
		text = "/task"
	}
	if b.Agent != nil && (strings.HasPrefix(text, "/memory") || strings.HasPrefix(text, "/task") || strings.HasPrefix(text, "/session")) {
		if text == "/session new" {
			b.clearSetup(chatID, userID)
			b.invalidateProductButtons(sessionKey{chatID, userID})
			if err := b.dropAssembly(sessionKey{chatID, userID}); err != nil {
				return err
			}
			b.forgetMaterialList(sessionKey{chatID, userID})
			b.invalidateSemantic(sessionKey{chatID, userID})
		}
		workshopID, err := b.WS.ActiveWorkshop(userID)
		if err != nil {
			return err
		}
		answer, _, err := b.Agent.HandleMessageForWorkshop(context.Background(), workshopID, userID, chatID, text)
		if err != nil {
			return err
		}
		return b.sendMessage(chatID, answer)
	}
	username := msg.From.Username
	if username == "" {
		username = msg.From.FirstName
	}
	if text == "/setup_stop" {
		if s := b.getSetup(chatID, userID); s != nil && s.kind == "product_edit" && s.composition != nil {
			return b.compositionMenu(sessionKey{chatID, userID}, s)
		}
		if s := b.getSetup(chatID, userID); s != nil && s.kind == "product_edit" {
			b.clearSetup(chatID, userID)
			return b.productsMenu(sessionKey{chatID, userID}, s.workshopID)
		}
		if err := b.dropAssembly(sessionKey{chatID, userID}); err != nil {
			return err
		}
		b.clearSetup(chatID, userID)
		return b.sendMessage(chatID, "Настройка остановлена. Введённые, но не сохранённые данные удалены из текущего диалога.")
	}
	materialAlias := strings.EqualFold(text, "материалы") || strings.EqualFold(text, "остатки")
	if strings.EqualFold(text, "/products") || (isProductsCommand(text) && b.getSetup(chatID, userID) == nil) {
		b.clearSetup(chatID, userID)
		workshop, err := b.WS.ActiveWorkshop(userID)
		if err != nil {
			return err
		}
		return b.productsMenu(sessionKey{chatID, userID}, workshop)
	}
	if text == "/materials" || (materialAlias && b.getSetup(chatID, userID) == nil) {
		workshopID, err := b.WS.ActiveWorkshop(userID)
		if err != nil {
			return err
		}
		return b.materialsMenu(sessionKey{chatID, userID}, workshopID)
	}
	if session := b.getSetup(chatID, userID); session != nil {
		workshopID, err := b.WS.ActiveWorkshop(userID)
		if err != nil || workshopID != session.workshopID {
			b.clearSetup(chatID, userID)
			return b.sendMessage(chatID, "Мастерская изменилась или доступ закрыт. Откройте меню заново: /materials.")
		}
		return b.handleSetupStep(chatID, text, session)
	}
	if handled, err := b.assemblyMessage(sessionKey{chatID, userID}, text); handled {
		return err
	}
	if handled, err := b.materialReference(sessionKey{chatID, userID}, text); handled {
		return err
	}
	if text == "/setup" {
		b.forgetMaterialList(sessionKey{chatID, userID})
		workshopID, err := b.WS.ActiveWorkshop(userID)
		if err != nil {
			return err
		}
		if err := auth.Require(b.WS.DB(), userID, workshopID, auth.WorkshopManage); err != nil {
			return err
		}
		if err := b.setupDatabase(); err != nil {
			return b.sendMessage(chatID, fmt.Sprintf("Ошибка инициализации базы данных: %v", err))
		}
		workshopID, err = b.ensureWorkshop(chatID, userID, msg)
		if err != nil {
			return err
		}
		materials, err := b.Inv.ForUser(userID).ListMaterials(workshopID)
		if err != nil {
			return err
		}
		products, err := b.Prod.ForUser(userID).ListProducts(workshopID)
		if err != nil {
			return err
		}
		return b.sendMessage(chatID, "База инициализирована без удаления данных.\n\n"+formatMaterials(materials)+"\n\n"+formatProducts(products)+"\n\nДля пошагового добавления материала отправьте /setup_add_material.\nДля пошагового добавления продукта отправьте /setup_add_product.\n\nДля выхода: /setup_stop")
	}
	if text == "/setup_add_material" {
		workshopID, err := b.ensureWorkshop(chatID, userID, msg)
		if err != nil {
			return err
		}
		if err := auth.Require(b.WS.DB(), userID, workshopID, auth.InventoryWrite); err != nil {
			return err
		}
		b.startSetup(chatID, &setupSession{kind: "material", stage: 0, workshopID: workshopID, userID: userID, actorName: username})
		return b.sendMessage(chatID, "Добавление материала начато. Введите название или /setup_stop для отмены.")
	}
	if text == "/setup_add_product" {
		workshopID, err := b.ensureWorkshop(chatID, userID, msg)
		if err != nil {
			return err
		}
		if err := auth.Require(b.WS.DB(), userID, workshopID, auth.ProductsWrite); err != nil {
			return err
		}
		b.startSetup(chatID, &setupSession{kind: "product", stage: 0, workshopID: workshopID, userID: userID, actorName: username})
		return b.sendMessage(chatID, "Добавление продукта начато. Введите название или /setup_stop для отмены.")
	}
	if strings.HasPrefix(text, "/materials") {
		workshopID, err := b.ensureWorkshop(chatID, userID, msg)
		if err != nil {
			return err
		}
		return b.materialsMenu(sessionKey{chatID, userID}, workshopID)
	}
	if text == "/to_order" || text == "/purchase" || text == "/buy" {
		workshopID, err := b.ensureWorkshop(chatID, userID, msg)
		if err != nil {
			return err
		}
		needs, err := b.Inv.ForUser(userID).ListPurchaseNeeds(workshopID)
		if err != nil {
			return err
		}
		return b.sendMessage(chatID, formatPurchaseNeeds(needs))
	}
	if strings.HasPrefix(text, "/stock ") {
		workshopID, err := b.ensureWorkshop(chatID, userID, msg)
		if err != nil {
			return err
		}
		name := strings.TrimSpace(strings.TrimPrefix(text, "/stock"))
		name = strings.TrimSpace(name)
		item, err := b.Inv.ForUser(userID).MaterialByName(workshopID, name)
		if err != nil {
			return err
		}
		b.selectMaterial(sessionKey{chatID, userID}, workshopID, item["id"].(int64))
		return b.sendMessage(chatID, fmt.Sprintf("Остаток %s: %s", name, inventory.Quantity(item, "current_stock")))
	}
	if strings.HasPrefix(text, "/audit") {
		workshopID, err := b.ensureWorkshop(chatID, userID, msg)
		if err != nil {
			return err
		}
		entries, err := b.Audit.ForUser(userID).ListByWorkshop(workshopID)
		if err != nil {
			return err
		}
		return b.sendMessage(chatID, formatAudit(entries))
	}
	if strings.HasPrefix(text, "/edit_material ") {
		workshopID, err := b.ensureWorkshop(chatID, userID, msg)
		if err != nil {
			return err
		}
		parts := strings.Fields(text)
		if len(parts) < 4 {
			return b.sendMessage(chatID, "Формат: /edit_material <название> <поле> <новое_значение>")
		}
		name := parts[1]
		field := parts[2]
		value := strings.Join(parts[3:], " ")
		materialID, err := b.Inv.ForUser(userID).GetMaterialID(workshopID, name)
		if err != nil {
			return err
		}
		if err := auth.Require(b.WS.DB(), userID, workshopID, auth.InventoryWrite); err != nil {
			return err
		}
		item, err := b.Inv.ForUser(userID).Material(workshopID, materialID)
		if err != nil {
			return err
		}
		unit := inventory.DisplayUnit(item)
		payload, _ := json.Marshal(map[string]string{"field": field, "value": value, "unit": unit})
		if field == "current_stock" || field == "minimum_stock" {
			value += " " + inventory.UnitLabel(unit)
		}
		return b.screen(sessionKey{chatID, userID}, fmt.Sprintf("Изменить материал %s: %s=%s?", name, field, value), choice{Text: "Подтвердить", Action: "edit_material", Workshop: workshopID, Target: materialID, Value: string(payload)}, choice{Text: "Отмена", Action: "home"})
	}
	if strings.HasPrefix(text, "/edit_product ") {
		workshopID, err := b.ensureWorkshop(chatID, userID, msg)
		if err != nil {
			return err
		}
		parts := strings.Fields(text)
		if len(parts) < 4 {
			return b.sendMessage(chatID, "Формат: /edit_product <название> <поле> <новое_значение>")
		}
		name := parts[1]
		field := parts[2]
		value := strings.Join(parts[3:], " ")
		productID, err := b.Prod.ForUser(userID).GetProductByName(workshopID, name)
		if err != nil {
			return err
		}
		if err := auth.Require(b.WS.DB(), userID, workshopID, auth.ProductsWrite); err != nil {
			return err
		}
		payload, _ := json.Marshal(map[string]string{"field": field, "value": value})
		return b.screen(sessionKey{chatID, userID}, fmt.Sprintf("Изменить продукт %s: %s=%s?", name, field, value), choice{Text: "Подтвердить", Action: "edit_product", Workshop: workshopID, Target: productID, Value: string(payload)}, choice{Text: "Отмена", Action: "home"})
	}
	if b.Agent != nil {
		workshopID, err := b.ensureWorkshop(chatID, userID, msg)
		if err != nil {
			return err
		}
		if handled, err := b.semanticMessage(sessionKey{chatID, userID}, workshopID, text); handled {
			return err
		}
		answer, usage, err := b.Agent.HandleMessageForWorkshop(context.Background(), workshopID, userID, chatID, text)
		if err != nil {
			return b.sendMessage(chatID, publicError(err)+"\n"+formatTokenUsage(usage))
		}
		return b.sendMessage(chatID, answer+"\n\n"+formatTokenUsage(usage))
	}
	return b.sendMessage(chatID, "Команда не распознана. Используйте /start для справки.")
}

func formatTokenUsage(usage *llm.Usage) string {
	if usage == nil || usage.TotalTokens == 0 {
		return "Токены LLM: 0 (локальная обработка)"
	}
	return fmt.Sprintf("Токены LLM: %d (вход: %d, ответ: %d)", usage.TotalTokens, usage.PromptTokens, usage.CompletionTokens)
}

func (b *Bot) setupDatabase() error {
	if err := b.Inv.Init(); err != nil {
		return fmt.Errorf("инициализация материалов: %w", err)
	}
	if err := b.Prod.Init(); err != nil {
		return fmt.Errorf("инициализация продуктов: %w", err)
	}
	for _, table := range storage.RequiredTables() {
		var name string
		if err := b.Inv.DB().QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name); err != nil {
			return fmt.Errorf("таблица %s: %w", table, err)
		}
	}
	return nil
}

func (b *Bot) startSetup(chatID int64, session *setupSession) {
	b.invalidateProductButtons(sessionKey{chatID, session.userID})
	b.setupMu.Lock()
	defer b.setupMu.Unlock()
	b.setup[sessionKey{chatID, session.userID}] = session
}

func (b *Bot) getSetup(chatID, userID int64) *setupSession {
	b.setupMu.Lock()
	defer b.setupMu.Unlock()
	return b.setup[sessionKey{chatID, userID}]
}

func (b *Bot) clearSetup(chatID, userID int64) {
	b.invalidateProductButtons(sessionKey{chatID, userID})
	b.setupMu.Lock()
	defer b.setupMu.Unlock()
	delete(b.setup, sessionKey{chatID, userID})
}

func (b *Bot) handleSetupStep(chatID int64, text string, session *setupSession) error {
	if session.kind == "product_edit" {
		return b.productEditStep(sessionKey{chatID, session.userID}, text, session)
	}
	if session.kind == "material_stock" || session.kind == "material_unit" {
		return b.materialStockStep(chatID, text, session)
	}
	permission := auth.InventoryWrite
	if session.kind == "product" {
		permission = auth.ProductsWrite
	}
	if err := auth.Require(b.WS.DB(), session.userID, session.workshopID, permission); err != nil {
		b.clearSetup(chatID, session.userID)
		return err
	}
	value := strings.TrimSpace(text)
	if value == "" {
		return b.sendMessage(chatID, "Значение не может быть пустым. Для отмены используйте /setup_stop.")
	}

	if session.kind == "material" {
		switch session.stage {
		case 0:
			if len([]rune(value)) > 200 {
				return b.sendMessage(chatID, "Название должно содержать не более 200 символов.")
			}
			session.name = value
			session.stage = 1
			return b.sendMessage(chatID, "Выберите категорию сырья, отправьте номер:\n1. Сырьё\n2. Компонент\n3. Инструмент\n4. Упаковка\n5. Другое")
		case 1:
			categories := []string{"сырьё", "компонент", "инструмент", "упаковка", "другое"}
			choice, err := parseChoice(value, len(categories))
			if err != nil {
				return b.sendMessage(chatID, "Введите номер категории от 1 до 5.")
			}
			session.category = categories[choice-1]
			session.stage = 2
			return b.sendMessage(chatID, "Выберите единицу измерения:\n1. г\n2. кг\n3. мл\n4. л\n5. шт")
		case 2:
			units := []string{"g", "kg", "ml", "l", "pcs"}
			choice, err := parseChoice(value, len(units))
			if err != nil {
				return b.sendMessage(chatID, "Введите номер единицы от 1 до 5.")
			}
			session.unit = units[choice-1]
			session.stage = 3
			return b.sendMessage(chatID, "Введите текущий остаток числом, например: 1000")
		case 3:
			stock, err := strconv.ParseFloat(strings.ReplaceAll(value, ",", "."), 64)
			if err != nil || stock < 0 || math.IsNaN(stock) || math.IsInf(inventory.ConvertToBase(session.unit, stock), 0) {
				return b.sendMessage(chatID, "Остаток должен быть неотрицательным числом.")
			}
			session.stock = stock
			session.stage = 4
			return b.sendMessage(chatID, "Введите минимальный остаток числом, например: 100")
		case 4:
			minimum, err := strconv.ParseFloat(strings.ReplaceAll(value, ",", "."), 64)
			if err != nil || minimum < 0 || math.IsNaN(minimum) || math.IsInf(inventory.ConvertToBase(session.unit, minimum), 0) {
				return b.sendMessage(chatID, "Минимальный остаток должен быть неотрицательным числом.")
			}
			session.minimum = minimum
			session.stage = 5
			return b.sendMessage(chatID, fmt.Sprintf("Проверьте:\nНазвание: %s\nКатегория: %s\nЕдиница: %s\nОстаток: %s\nМинимум: %s\n\nВведите да для сохранения или нет для отмены.", session.name, session.category, inventory.UnitLabel(session.unit), inventory.FormatQuantity(inventory.ConvertToBase(session.unit, session.stock), session.unit), inventory.FormatQuantity(inventory.ConvertToBase(session.unit, session.minimum), session.unit)))
		case 5:
			if isNo(value) {
				b.clearSetup(chatID, session.userID)
				return b.sendMessage(chatID, "Добавление материала отменено. Данные не изменялись.")
			}
			if !isYes(value) {
				return b.sendMessage(chatID, "Введите да для сохранения или нет для отмены.")
			}
			id, err := b.Inv.ForUser(session.userID).CreateMaterial(session.workshopID, session.name, session.category, session.unit, session.stock, session.minimum, "", 0, "")
			if err != nil {
				return err
			}
			b.clearSetup(chatID, session.userID)
			if err := b.sendMessage(chatID, fmt.Sprintf("Материал сохранён, id=%d.", id)); err != nil {
				return err
			}
			return b.materialsMenu(sessionKey{chatID, session.userID}, session.workshopID)
		}
	}

	switch session.stage {
	case 0:
		session.name = value
		session.stage = 1
		return b.sendMessage(chatID, "Введите артикул (SKU) или - если он не нужен.")
	case 1:
		session.sku = value
		if value == "-" {
			session.sku = ""
		}
		session.stage = 2
		return b.sendMessage(chatID, "Выберите тип продукта:\n1. Готовое изделие\n2. Набор\n3. Полуфабрикат")
	case 2:
		productTypes := []string{"product", "kit", "semi_finished"}
		choice, err := parseChoice(value, len(productTypes))
		if err != nil {
			return b.sendMessage(chatID, "Введите номер типа от 1 до 3.")
		}
		session.productType = productTypes[choice-1]
		session.stage = 3
		return b.sendMessage(chatID, "Введите текущий остаток числом.")
	case 3:
		stock, err := strconv.ParseFloat(strings.ReplaceAll(value, ",", "."), 64)
		if err != nil || stock < 0 || math.IsNaN(stock) || math.IsInf(stock, 0) || stock > 1e12 {
			return b.sendMessage(chatID, "Остаток должен быть неотрицательным числом.")
		}
		session.stock = stock
		session.stage = 4
		return b.sendMessage(chatID, "Введите минимальный остаток числом.")
	case 4:
		minimum, err := strconv.ParseFloat(strings.ReplaceAll(value, ",", "."), 64)
		if err != nil || minimum < 0 || math.IsNaN(minimum) || math.IsInf(minimum, 0) || minimum > 1e12 {
			return b.sendMessage(chatID, "Минимальный остаток должен быть неотрицательным числом.")
		}
		session.minimum = minimum
		session.stage = 5
		return b.sendMessage(chatID, fmt.Sprintf("Проверьте:\nНазвание: %s\nSKU: %s\nТип: %s\nОстаток: %g шт\nМинимум: %g шт\n\nВведите да для сохранения или нет для отмены.", session.name, session.sku, products.TypeLabel(session.productType), session.stock, session.minimum))
	case 5:
		if isNo(value) {
			b.clearSetup(chatID, session.userID)
			return b.sendMessage(chatID, "Добавление продукта отменено. Данные не изменялись.")
		}
		if !isYes(value) {
			return b.sendMessage(chatID, "Введите да для сохранения или нет для отмены.")
		}
		id, err := b.Prod.ForUser(session.userID).CreateProduct(session.workshopID, session.name, session.sku, session.productType, session.stock, session.minimum, "")
		if err != nil {
			return err
		}
		b.clearSetup(chatID, session.userID)
		if err := b.sendMessage(chatID, fmt.Sprintf("Продукт сохранён, id=%d.", id)); err != nil {
			return err
		}
		return b.productsMenu(sessionKey{chatID, session.userID}, session.workshopID)
	}
	return nil
}

func parseChoice(value string, max int) (int, error) {
	choice, err := strconv.Atoi(value)
	if err != nil || choice < 1 || choice > max {
		return 0, fmt.Errorf("choice must be between 1 and %d", max)
	}
	return choice, nil
}

func isYes(value string) bool {
	return value == "да" || value == "д" || strings.EqualFold(value, "yes") || value == "1"
}

func isNo(value string) bool {
	return value == "нет" || value == "н" || strings.EqualFold(value, "no") || value == "0"
}

func (b *Bot) ensureWorkshop(chatID, userID int64, msg *telegramMessage) (int64, error) {
	return b.WS.ActiveWorkshop(userID)
}

func (b *Bot) handleMessage(msg *telegramMessage) error {
	err := b.processMessage(msg)
	if errors.Is(err, auth.ErrChooseWorkshop) && msg != nil {
		var userID int64
		if lookupErr := b.WS.DB().QueryRow(`SELECT id FROM users WHERE telegram_user_id=?`, msg.From.ID).Scan(&userID); lookupErr == nil {
			return b.switcher(sessionKey{msg.Chat.ID, userID})
		}
	}
	if err != nil && msg != nil {
		return b.sendMessage(msg.Chat.ID, publicError(err))
	}
	return err
}

func (b *Bot) sendMessage(chatID int64, text string) error {
	if i := strings.LastIndex(text, "\nТокены LLM:"); i >= 0 {
		var user int64
		if err := b.WS.DB().QueryRow("SELECT id FROM users WHERE telegram_user_id=?", chatID).Scan(&user); err == nil {
			if p, err := personalization.New(b.WS.DB()).ForUser(user).GetProfile(); err == nil && p.HideLLM {
				text = strings.TrimSpace(text[:i])
			}
		}
	}
	if text == "" {
		return nil
	}
	for _, part := range materialMessageParts(text) {
		if err := b.api("sendMessage", map[string]any{"chat_id": chatID, "text": part, "disable_web_page_preview": true}, nil); err != nil {
			return err
		}
	}
	return nil
}

func urlQueryEscape(s string) string {
	return strings.ReplaceAll(s, "\"", "%22")
}

func formatMaterials(items []map[string]any) string {
	if len(items) == 0 {
		return "Материалы не найдены."
	}
	lines := []string{"Материалы:"}
	for _, item := range items {
		lines = append(lines, fmt.Sprintf("- %v: %s", item["name"], inventory.Quantity(item, "current_stock")))
	}
	return strings.Join(lines, "\n")
}

func formatProducts(items []map[string]any) string {
	if len(items) == 0 {
		return "Продукты не найдены."
	}
	lines := []string{"Продукты:"}
	for _, item := range items {
		lines = append(lines, fmt.Sprintf("- %v: %.2f", item["name"], item["current_stock"]))
	}
	return strings.Join(lines, "\n")
}

func formatPurchaseNeeds(items []map[string]any) string {
	if len(items) == 0 {
		return "Закупка не требуется: все материалы выше минимального остатка."
	}
	lines := []string{"Нужно заказать:"}
	for _, item := range items {
		line := fmt.Sprintf("- %v: заказать %s (сейчас %s, минимум %s)", item["name"], inventory.Quantity(item, "order_quantity"), inventory.Quantity(item, "current_stock"), inventory.Quantity(item, "minimum_stock"))
		if supplier := strings.TrimSpace(fmt.Sprint(item["supplier"])); supplier != "" {
			line += ", поставщик: " + supplier
		}
		if leadTime := item["lead_time_days"]; leadTime != 0 {
			line += fmt.Sprintf(", срок: %v дн.", leadTime)
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func formatAudit(entries []map[string]any) string {
	if len(entries) == 0 {
		return "Аудит пуст."
	}
	lines := []string{"Последние изменения:"}
	for _, e := range entries {
		field := fmt.Sprint(e["field_name"])
		if field == "" {
			field = "-"
		}
		lines = append(lines, fmt.Sprintf("- %s %s: %s -> %s (%s)", e["entity_type"], e["action"], field, e["new_value"], e["created_at"]))
	}
	return strings.Join(lines, "\n")
}

type telegramResponse struct {
	OK     bool             `json:"ok"`
	Result []telegramUpdate `json:"result"`
}

type telegramUpdate struct {
	UpdateID      int64             `json:"update_id"`
	CallbackQuery *telegramCallback `json:"callback_query"`
	Message       *telegramMessage  `json:"message"`
}

type telegramMessage struct {
	Date      int64           `json:"date"`
	Dice      json.RawMessage `json:"dice"`
	MessageID int64           `json:"message_id"`
	Text      string          `json:"text"`
	Chat      telegramChat    `json:"chat"`
	From      telegramUser    `json:"from"`
}

type telegramChat struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
	Type  string `json:"type"`
}

type telegramUser struct {
	ID        int64  `json:"id"`
	Username  string `json:"username"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
}
