package telegram

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"workshop-agent/internal/audit"
	"workshop-agent/internal/inventory"
	"workshop-agent/internal/products"
	"workshop-agent/internal/storage"
	"workshop-agent/internal/workshops"
)

type Bot struct {
	Token      string
	WS         *workshops.Service
	Inv        *inventory.Service
	Prod       *products.Service
	Audit      *audit.Service
	Started    bool
	HTTPClient *http.Client
	setupMu    sync.Mutex
	setup      map[int64]*setupSession
}

type setupSession struct {
	kind         string
	stage        int
	workshopID   int64
	userID       int64
	actorName    string
	name         string
	category     string
	unit         string
	stock        float64
	minimum      float64
	sku          string
	productType  string
}

func NewBot(token string, ws *workshops.Service, inv *inventory.Service, prod *products.Service, auditSvc *audit.Service) (*Bot, error) {
	if token == "" {
		return nil, logError("TELEGRAM_BOT_TOKEN is empty")
	}
	return &Bot{Token: token, WS: ws, Inv: inv, Prod: prod, Audit: auditSvc, Started: true, HTTPClient: &http.Client{Timeout: 15 * time.Second}, setup: make(map[int64]*setupSession)}, nil
}

func logError(msg string) error {
	return &botError{msg: msg}
}

type botError struct { msg string }

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
			if update.Message == nil || strings.TrimSpace(update.Message.Text) == "" {
				continue
			}
			if err := b.handleMessage(update.Message); err != nil {
				log.Printf("handle message error: %v", err)
			}
		}
		if len(updates) == 0 {
			time.Sleep(1 * time.Second)
		}
	}
}

func (b *Bot) getUpdates(offset int64) ([]telegramUpdate, error) {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/getUpdates?offset=%d&timeout=30&allowed_updates=%s", b.Token, offset, urlQueryEscape("[\"message\"]"))
	resp, err := b.HTTPClient.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("telegram getUpdates failed: status=%s body=%s", resp.Status, string(body))
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

func (b *Bot) handleMessage(msg *telegramMessage) error {
	if msg == nil {
		return nil
	}
	text := strings.TrimSpace(msg.Text)
	if text == "" {
		return nil
	}
	chatID := msg.Chat.ID
	userID := msg.From.ID
	username := msg.From.Username
	if username == "" {
		username = msg.From.FirstName
	}
	if text == "/setup_stop" {
		b.clearSetup(chatID)
		return b.sendMessage(chatID, "Настройка остановлена. Введённые, но не сохранённые данные удалены из текущего диалога.")
	}
	if session := b.getSetup(chatID); session != nil {
		return b.handleSetupStep(chatID, text, session)
	}
	if strings.HasPrefix(text, "/start") {
		if _, err := b.WS.RegisterUser(userID, username, msg.From.FirstName+" "+msg.From.LastName); err != nil {
			return err
		}
		workshopID, err := b.WS.GetWorkshopByChat(chatID)
		if err != nil || workshopID == 0 {
			workshopID, err = b.WS.CreateWorkshop("Мастерская demo")
			if err != nil {
				return err
			}
		}
		if err := b.WS.EnsureTelegramChat(chatID, workshopID, msg.Chat.Title); err != nil {
			return err
		}
		if err := b.WS.AddMember(workshopID, userID, "owner"); err != nil {
			return err
		}
		return b.sendMessage(chatID, "Мастерская готова. Доступные команды: /setup, /materials, /products, /to_order, /stock <название>, /edit_material <название> <поле> <значение>, /edit_product <название> <поле> <значение>, /audit")
	}
	if text == "/setup" {
		if err := b.setupDatabase(); err != nil {
			return b.sendMessage(chatID, fmt.Sprintf("Ошибка инициализации базы данных: %v", err))
		}
		workshopID, err := b.ensureWorkshop(chatID, userID, msg)
		if err != nil {
			return err
		}
		materials, err := b.Inv.ListMaterials(workshopID)
		if err != nil {
			return err
		}
		products, err := b.Prod.ListProducts(workshopID)
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
		b.startSetup(chatID, &setupSession{kind: "material", stage: 0, workshopID: workshopID, userID: userID, actorName: username})
		return b.sendMessage(chatID, "Добавление материала начато. Введите название или /setup_stop для отмены.")
	}
	if text == "/setup_add_product" {
		workshopID, err := b.ensureWorkshop(chatID, userID, msg)
		if err != nil {
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
		materials, err := b.Inv.ListMaterials(workshopID)
		if err != nil {
			return err
		}
		return b.sendMessage(chatID, formatMaterials(materials))
	}
	if strings.HasPrefix(text, "/products") {
		workshopID, err := b.ensureWorkshop(chatID, userID, msg)
		if err != nil {
			return err
		}
		products, err := b.Prod.ListProducts(workshopID)
		if err != nil {
			return err
		}
		return b.sendMessage(chatID, formatProducts(products))
	}
	if text == "/to_order" || text == "/purchase" || text == "/buy" {
		workshopID, err := b.ensureWorkshop(chatID, userID, msg)
		if err != nil {
			return err
		}
		needs, err := b.Inv.ListPurchaseNeeds(workshopID)
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
		stock, err := b.Inv.GetMaterialStock(workshopID, name)
		if err != nil {
			return err
		}
		return b.sendMessage(chatID, fmt.Sprintf("Остаток %s: %.2f", name, stock))
	}
	if strings.HasPrefix(text, "/audit") {
		workshopID, err := b.ensureWorkshop(chatID, userID, msg)
		if err != nil {
			return err
		}
		entries, err := b.Audit.ListByWorkshop(workshopID)
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
		materialID, err := b.Inv.GetMaterialID(workshopID, name)
		if err != nil {
			return err
		}
		if err := b.Inv.UpdateMaterial(workshopID, materialID, field, value, userID, username); err != nil {
			return err
		}
		return b.sendMessage(chatID, fmt.Sprintf("Материал %s обновлён: %s=%s", name, field, value))
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
		productID, err := b.Prod.GetProductByName(workshopID, name)
		if err != nil {
			return err
		}
		if err := b.Prod.UpdateProduct(workshopID, productID, field, value, userID, username); err != nil {
			return err
		}
		return b.sendMessage(chatID, fmt.Sprintf("Продукт %s обновлён: %s=%s", name, field, value))
	}
	return b.sendMessage(chatID, "Команда не распознана. Используйте /start для справки.")
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
	b.setupMu.Lock()
	defer b.setupMu.Unlock()
	b.setup[chatID] = session
}

func (b *Bot) getSetup(chatID int64) *setupSession {
	b.setupMu.Lock()
	defer b.setupMu.Unlock()
	return b.setup[chatID]
}

func (b *Bot) clearSetup(chatID int64) {
	b.setupMu.Lock()
	defer b.setupMu.Unlock()
	delete(b.setup, chatID)
}

func (b *Bot) handleSetupStep(chatID int64, text string, session *setupSession) error {
	value := strings.TrimSpace(text)
	if value == "" {
		return b.sendMessage(chatID, "Значение не может быть пустым. Для отмены используйте /setup_stop.")
	}

	if session.kind == "material" {
		switch session.stage {
		case 0:
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
			stock, err := strconv.ParseFloat(value, 64)
			if err != nil || stock < 0 {
				return b.sendMessage(chatID, "Остаток должен быть неотрицательным числом.")
			}
			session.stock = stock
			session.stage = 4
			return b.sendMessage(chatID, "Введите минимальный остаток числом, например: 100")
		case 4:
			minimum, err := strconv.ParseFloat(value, 64)
			if err != nil || minimum < 0 {
				return b.sendMessage(chatID, "Минимальный остаток должен быть неотрицательным числом.")
			}
			session.minimum = minimum
			session.stage = 5
			return b.sendMessage(chatID, fmt.Sprintf("Проверьте:\nНазвание: %s\nКатегория: %s\nЕдиница: %s\nОстаток: %.2f\nМинимум: %.2f\n\nВведите да для сохранения или нет для отмены.", session.name, session.category, session.unit, session.stock, session.minimum))
		case 5:
			if isNo(value) {
				b.clearSetup(chatID)
				return b.sendMessage(chatID, "Добавление материала отменено. Данные не изменялись.")
			}
			if !isYes(value) {
				return b.sendMessage(chatID, "Введите да для сохранения или нет для отмены.")
			}
			id, err := b.Inv.CreateMaterial(session.workshopID, session.name, session.category, session.unit, session.stock, session.minimum, "", 0, "")
			if err != nil {
				return err
			}
			b.clearSetup(chatID)
			return b.sendMessage(chatID, fmt.Sprintf("Материал сохранён, id=%d.", id))
		}
	}

	switch session.stage {
	case 0:
		session.name = value
		session.stage = 1
		return b.sendMessage(chatID, "Введите артикул (SKU) или - если он не нужен.")
	case 1:
		session.sku = value
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
		stock, err := strconv.ParseFloat(value, 64)
		if err != nil || stock < 0 {
			return b.sendMessage(chatID, "Остаток должен быть неотрицательным числом.")
		}
		session.stock = stock
		session.stage = 4
		return b.sendMessage(chatID, "Введите минимальный остаток числом.")
	case 4:
		minimum, err := strconv.ParseFloat(value, 64)
		if err != nil || minimum < 0 {
			return b.sendMessage(chatID, "Минимальный остаток должен быть неотрицательным числом.")
		}
		session.minimum = minimum
		session.stage = 5
		return b.sendMessage(chatID, fmt.Sprintf("Проверьте:\nНазвание: %s\nSKU: %s\nТип: %s\nОстаток: %.2f\nМинимум: %.2f\n\nВведите да для сохранения или нет для отмены.", session.name, session.sku, session.productType, session.stock, session.minimum))
	case 5:
		if isNo(value) {
			b.clearSetup(chatID)
			return b.sendMessage(chatID, "Добавление продукта отменено. Данные не изменялись.")
		}
		if !isYes(value) {
			return b.sendMessage(chatID, "Введите да для сохранения или нет для отмены.")
		}
		id, err := b.Prod.CreateProduct(session.workshopID, session.name, session.sku, session.productType, session.stock, session.minimum, "")
		if err != nil {
			return err
		}
		b.clearSetup(chatID)
		return b.sendMessage(chatID, fmt.Sprintf("Продукт сохранён, id=%d.", id))
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
	if workshopID, err := b.WS.GetWorkshopByChat(chatID); err == nil && workshopID > 0 {
		return workshopID, nil
	}
	workshopID, err := b.WS.CreateWorkshop("Мастерская demo")
	if err != nil {
		return 0, err
	}
	if _, err := b.WS.RegisterUser(userID, msg.From.Username, msg.From.FirstName+" "+msg.From.LastName); err != nil {
		return 0, err
	}
	if err := b.WS.EnsureTelegramChat(chatID, workshopID, msg.Chat.Title); err != nil {
		return 0, err
	}
	if err := b.WS.AddMember(workshopID, userID, "owner"); err != nil {
		return 0, err
	}
	return workshopID, nil
}

func (b *Bot) sendMessage(chatID int64, text string) error {
	if text == "" {
		return nil
	}
	body, err := json.Marshal(map[string]any{"chat_id": chatID, "text": text, "parse_mode": "HTML"})
	if err != nil {
		return err
	}
	resp, err := b.HTTPClient.Post(fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", b.Token), "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("telegram sendMessage failed: status=%s body=%s", resp.Status, string(payload))
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
		lines = append(lines, fmt.Sprintf("- %v: %.2f %v", item["name"], item["current_stock"], item["base_unit"]))
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
		line := fmt.Sprintf("- %v: заказать %.2f %v (сейчас %.2f, минимум %.2f)", item["name"], item["order_quantity"], item["base_unit"], item["current_stock"], item["minimum_stock"])
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
	UpdateID int64            `json:"update_id"`
	Message  *telegramMessage `json:"message"`
}

type telegramMessage struct {
	MessageID int64        `json:"message_id"`
	Text      string       `json:"text"`
	Chat      telegramChat `json:"chat"`
	From      telegramUser `json:"from"`
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
