package telegram

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"workshop-agent/internal/agent"
	"workshop-agent/internal/auth"
	"workshop-agent/internal/inventory"
	"workshop-agent/internal/memory"
	"workshop-agent/internal/products"
)

// Drafts use pending_actions; confirmed tasks use the existing working memory.
type assemblyDraft struct {
	Assignee   int64
	Revision   int
	Candidates []int64
	Query      string
	ID         int64
	Session    int64
	Orders     int64
	PerOrder   int64
	Quantity   int64
	Product    int64
	Stage      string
}

func isProductsCommand(text string) bool {
	switch strings.ToLower(strings.TrimSpace(text)) {
	case "/products", "товары", "продукты", "изделия", "список товаров", "покажи товары", "покажи продукты":
		return true
	}
	return false
}

func (b *Bot) dropAssembly(key sessionKey) error {
	b.forgetMaterialList(key)
	_, err := b.WS.DB().Exec("DELETE FROM pending_actions WHERE user_id=? AND chat_id=? AND action_type='assembly_draft'", key.UserID, key.ChatID)
	return err
}

func (b *Bot) loadAssembly(key sessionKey, workshop int64) (*assemblyDraft, error) {
	var d assemblyDraft
	var raw string
	err := b.WS.DB().QueryRow("SELECT id,payload FROM pending_actions WHERE user_id=? AND chat_id=? AND workshop_id=? AND action_type='assembly_draft' AND expires_at>? ORDER BY id DESC LIMIT 1",
		key.UserID, key.ChatID, workshop, time.Now().UTC().Format(time.RFC3339)).Scan(&d.ID, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	id := d.ID
	if err = json.Unmarshal([]byte(raw), &d); err != nil {
		return nil, err
	}
	d.ID = id
	var active int
	err = b.WS.DB().QueryRow("SELECT COUNT(*) FROM conversation_sessions WHERE id=? AND user_id=? AND workshop_id=? AND telegram_chat_id=? AND status='active'", d.Session, key.UserID, workshop, key.ChatID).Scan(&active)
	if err != nil {
		return nil, err
	}
	if active != 1 {
		return nil, b.dropAssembly(key)
	}
	return &d, nil
}

func (b *Bot) storeAssembly(key sessionKey, workshop int64, d *assemblyDraft) error {
	d.Revision++
	raw, err := json.Marshal(d)
	if err != nil {
		return err
	}
	if d.ID != 0 {
		r, err := b.WS.DB().Exec("UPDATE pending_actions SET payload=? WHERE id=? AND user_id=? AND workshop_id=? AND chat_id=? AND action_type='assembly_draft'", string(raw), d.ID, key.UserID, workshop, key.ChatID)
		if err != nil {
			return err
		}
		n, err := r.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return errors.New("Черновик устарел. Начните сборку заново.")
		}
		return nil
	}
	if err = b.dropAssembly(key); err != nil {
		return err
	}
	r, err := b.WS.DB().Exec("INSERT INTO pending_actions(user_id,workshop_id,chat_id,action_type,payload,expires_at) VALUES(?,?,?,'assembly_draft',?,?)", key.UserID, workshop, key.ChatID, string(raw), time.Now().Add(15*time.Minute).UTC().Format(time.RFC3339))
	if err != nil {
		return err
	}
	d.ID, err = r.LastInsertId()
	return err
}

func (b *Bot) productsMenu(key sessionKey, workshop int64) error {
	b.invalidateProductButtons(key)
	items, err := b.Prod.ForUser(key.UserID).ListProducts(workshop)
	if err != nil {
		return err
	}
	b.forgetMaterialList(key)
	ids := make([]int64, 0, len(items))
	chunk := "Продукты:\n"
	for i, item := range items {
		line := fmt.Sprintf("%d. %s — %g шт\n", i+1, item["name"], item["current_stock"])
		// Telegram length is measured in UTF-16; a 3500-byte chunk is conservative.
		for len(chunk)+len(line) > 3500 {
			if len(chunk) > 0 {
				if err = b.sendMessage(key.ChatID, chunk); err != nil {
					return err
				}
				chunk = ""
			}
			if len(line) <= 3500 {
				break
			}
			runes := []rune(line)
			n := 700
			if len(runes) < n {
				n = len(runes)
			}
			if err = b.sendMessage(key.ChatID, string(runes[:n])); err != nil {
				return err
			}
			line = string(runes[n:])
		}
		chunk += line
		ids = append(ids, item["id"].(int64))
	}
	choices := []choice{}
	if len(items) == 0 {
		chunk = "Продуктов пока нет."
	}
	if auth.Require(b.WS.DB(), key.UserID, workshop, auth.ProductsWrite) == nil {
		choices = append(choices, choice{Text: "Добавить продукт", Action: "product_add", Workshop: workshop})
		if len(items) > 0 {
			choices = append(choices, choice{Text: "Редактировать продукт", Action: "product_edit_open", Workshop: workshop})
		}
	} else if len(items) > 0 {
		choices = append(choices, choice{Text: "Просмотреть продукт", Action: "product_edit_open", Workshop: workshop})
	}
	d, err := b.loadAssembly(key, workshop)
	if err != nil {
		return err
	}
	if d != nil && d.Stage == "product" && b.getSetup(key.ChatID, key.UserID) == nil {
		chunk += "\nВыберите номер или название продукта для текущей сборки. Отмена: /setup_stop."
	}
	if err = b.screen(key, chunk, choices...); err != nil {
		return err
	}
	b.rememberMaterialList(key, workshop, ids)
	b.uiMu.Lock()
	s := b.materialLists[key]
	s.Kind = "product"
	b.materialLists[key] = s
	b.uiMu.Unlock()
	return nil
}

var assemblyStart = regexp.MustCompile(`(?i)^(?:собер[её]м|собрать|собираем|собери|задача сборки|сделаем|нужно собрать)\s+(.+)$`)

func pieceCount(text string) (int64, error) {
	if !regexp.MustCompile(`^[0-9]+$`).MatchString(text) {
		return 0, errors.New("Введите целое число от 1 до 1000000000.")
	}
	n, err := strconv.ParseInt(text, 10, 64)
	if err != nil || n < 1 || n > 1000000000 {
		return 0, errors.New("Введите целое число от 1 до 1000000000.")
	}
	return n, nil
}
func productPosition(text string) int {
	if n, err := strconv.Atoi(text); err == nil {
		return n
	}
	return map[string]int{"первый": 1, "второй": 2, "третий": 3, "четвертый": 4, "четвёртый": 4, "пятый": 5, "шестой": 6, "седьмой": 7, "восьмой": 8, "девятый": 9, "десятый": 10}[strings.ToLower(text)]
}
func productWords(text string) string {
	text = strings.ReplaceAll(strings.ToLower(text), "ё", "е")
	words := strings.FieldsFunc(text, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	forms := map[string]string{
		"дефлекторов": "дефлектор", "дефлекторы": "дефлектор", "дефлектора": "дефлектор",
		"фигурок": "фигурка", "фигурки": "фигурка",
		"наборов": "набор", "наборы": "набор", "набора": "набор",
		"сливов": "слив", "сливы": "слив", "слива": "слив", "сливом": "слив", "сливу": "слив",
	}
	for i, word := range words {
		if normal, ok := forms[word]; ok {
			words[i] = normal
		}
	}
	return strings.Join(words, " ")
}

// Match complete normalized words, allowing a shortened product name while
// retaining all matching candidates for the caller's ambiguity check.
func productNameMatches(name, mention string) bool {
	nameWords := strings.Fields(productWords(name))
	queryWords := strings.Fields(productWords(mention))
	if len(queryWords) == 0 {
		return false
	}
	for _, word := range queryWords {
		found := false
		for _, candidate := range nameWords {
			if word == candidate {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
func (b *Bot) assemblyMessage(key sessionKey, text string) (bool, error) {
	if b.Agent == nil || b.Agent.Memory == nil || strings.HasPrefix(text, "/") {
		return false, nil
	}
	match := assemblyStart.FindStringSubmatch(text)
	if match == nil {
		if agent.LocalReadCommand(text) != nil {
			return false, nil
		}
		if stockReference.MatchString(strings.ToLower(text)) {
			return false, nil
		}
	}
	workshop, err := b.WS.ActiveWorkshop(key.UserID)
	if err != nil {
		if match != nil {
			return true, err
		}
		return false, nil
	}
	d, err := b.loadAssembly(key, workshop)
	if err != nil {
		return true, err
	}
	if d == nil && match == nil {
		return false, nil
	}
	for _, p := range []auth.Permission{auth.TasksCreate, auth.ProductsRead} {
		if err = auth.Require(b.WS.DB(), key.UserID, workshop, p); err != nil {
			return true, err
		}
	}
	if match != nil {
		fields := strings.Fields(match[1])
		n, e := pieceCount(fields[0])
		if e != nil {
			if regexp.MustCompile(`^[+\-0-9]|(?i)^(nan|inf)`).MatchString(fields[0]) {
				return true, b.sendMessage(key.ChatID, e.Error())
			}
			return true, b.beginAssembly(key, workshop, 0, 0, match[1])
		}
		sc, e := b.Agent.Memory.ForUser(key.UserID).EnsureSession(memory.Scope{UserID: key.UserID, WorkshopID: workshop}, key.ChatID)
		if e != nil {
			return true, e
		}
		d = &assemblyDraft{Session: sc.SessionID, Quantity: n, Stage: "product"}
		name := strings.Join(fields[1:], " ")
		if strings.HasPrefix(strings.ToLower(name), "заказ") {
			d.Orders = n
			d.Quantity = 0
			name = ""
		}
		if err = b.storeAssembly(key, workshop, d); err != nil {
			return true, err
		}
		lower := strings.ToLower(text)
		if strings.HasPrefix(lower, "собрать ") || strings.HasPrefix(lower, "сделаем ") || strings.HasPrefix(lower, "нужно собрать ") {
			return true, b.beginAssembly(key, workshop, d.Quantity, d.Orders, name)
		}
		if name == "" {
			return true, b.assemblyCatalog(key, workshop, d, "", 0)
		}
		text = name
	}
	if d.Stage == "confirm" {
		if corrected := regexp.MustCompile(`(?i)^нет,?\s*(?:сделай\s*)?([0-9]+)$`).FindStringSubmatch(strings.TrimRight(text, ".!")); corrected != nil {
			n, e := pieceCount(corrected[1])
			if e != nil {
				return true, b.sendMessage(key.ChatID, e.Error())
			}
			d.Quantity = n
			d.Orders = 0
			d.PerOrder = 0
		}
		return true, b.previewAssembly(key, workshop, d)
	}
	if d.Stage == "executor" {
		return true, b.sendMessage(key.ChatID, "Выберите исполнителя кнопкой. Отмена: /setup_stop.")
	}
	if d.Stage == "quantity" {
		n, e := pieceCount(text)
		if e != nil {
			return true, b.sendMessage(key.ChatID, e.Error())
		}
		if d.Orders > 0 {
			if n > 1000000000/d.Orders {
				return true, b.sendMessage(key.ChatID, "Итог превышает 1000000000 шт. Укажите меньшее количество.")
			}
			d.PerOrder = n
			d.Quantity = d.Orders * n
		} else {
			d.Quantity = n
		}
	} else {
		items, e := b.Prod.ForUser(key.UserID).ListProducts(workshop)
		if e != nil {
			return true, e
		}
		n := productPosition(text)
		selected := int64(0)
		if n != 0 || regexp.MustCompile(`^[+-]?[0-9]+$`).MatchString(text) {
			snap := b.currentList(key, workshop)
			if snap.Kind != "product" || n < 1 || n > len(snap.IDs) {
				return true, b.sendMessage(key.ChatID, "Нет такого номера в актуальном списке продуктов. Откройте товары и выберите номер.")
			}
			selected = snap.IDs[n-1]
		} else {
			matches := []int64{}
			for _, p := range items {
				if productNameMatches(p["name"].(string), text) {
					matches = append(matches, p["id"].(int64))
				}
			}
			if len(matches) != 1 {
				return true, b.assemblyCatalog(key, workshop, d, text, 0)
			}
			selected = matches[0]
		}
		found := false
		name := ""
		for _, p := range items {
			if p["id"].(int64) == selected {
				found = true
				name = p["name"].(string)
			}
		}
		if !found {
			return true, b.sendMessage(key.ChatID, "Продукт больше недоступен. Откройте товары заново.")
		}
		d.Product = selected
		if d.Orders > 0 || d.Quantity == 0 {
			d.Stage = "quantity"
			if err = b.storeAssembly(key, workshop, d); err != nil {
				return true, err
			}
			question := "Сколько единиц нужно собрать?"
			if d.Orders > 0 {
				question = "Сколько штук входит в один заказ?"
			}
			return true, b.sendMessage(key.ChatID, fmt.Sprintf("Выбран продукт «%s». %s Отмена: /setup_stop.", name, question))
		}
	}
	return true, b.previewAssembly(key, workshop, d)
}

func (b *Bot) previewAssembly(key sessionKey, workshop int64, d *assemblyDraft) error {
	if d.Assignee == 0 {
		return b.chooseDraftExecutor(key, workshop, d)
	}
	if err := auth.Require(b.WS.DB(), d.Assignee, workshop, auth.TasksExecute); err != nil {
		return err
	}

	items, err := b.Prod.ForUser(key.UserID).ListProducts(workshop)
	if err != nil {
		return err
	}
	name := ""
	for _, p := range items {
		if p["id"].(int64) == d.Product {
			name = p["name"].(string)
		}
	}
	if name == "" {
		return auth.ErrDenied
	}
	if err = auth.Require(b.WS.DB(), key.UserID, workshop, auth.InventoryRead); err != nil {
		return err
	}
	needs, err := b.Prod.ForUser(key.UserID).Requirements(workshop, d.Product, float64(d.Quantity))
	if err != nil && !errors.Is(err, products.ErrIncompleteBOM) {
		return err
	}
	text := fmt.Sprintf("Задача сборки: %s — %d шт.", name, d.Quantity)
	var workshopName string
	if err = b.WS.DB().QueryRow("SELECT name FROM workshops WHERE id=?", workshop).Scan(&workshopName); err != nil {
		return err
	}
	text += "\nМастерская: " + workshopName
	if d.Orders > 0 {
		text += fmt.Sprintf("\n%d заказов × %d шт = %d шт.", d.Orders, d.PerOrder, d.Quantity)
	}
	if err != nil || len(needs) == 0 {
		text += "\nBOM отсутствует или недостаточен. Потребность и дефицит рассчитать нельзя."
	} else {
		ids := []int64{}
		for id := range needs {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		for _, id := range ids {
			p, e := b.Inv.ForUser(key.UserID).Material(workshop, id)
			if e != nil {
				return e
			}
			qty := needs[id]
			if math.IsNaN(qty) || math.IsInf(qty, 0) || qty < 0 {
				return errors.New("Некорректный расчёт BOM")
			}
			deficit := math.Max(0, qty-p["current_stock"].(float64))
			unit := inventory.DisplayUnit(p)
			text += fmt.Sprintf("\n%s: нужно %s; есть %s; дефицит %s.", p["name"], inventory.FormatQuantity(qty, unit), inventory.Quantity(p, "current_stock"), inventory.FormatQuantity(deficit, unit))
		}
	}
	d.Stage = "confirm"
	if err = b.storeAssembly(key, workshop, d); err != nil {
		return err
	}
	for len([]rune(text)) > 1500 {
		r := []rune(text)
		if err = b.sendMessage(key.ChatID, string(r[:1500])); err != nil {
			return err
		}
		text = string(r[1500:])
	}
	text += b.assignmentText(key, workshop, key.UserID, d.Assignee)
	value := fmt.Sprint(d.Revision)
	return b.screen(key, text+"\nСоздать задачу? Склад не изменится.", choice{Text: "Создать задачу", Action: "assembly_save", Workshop: workshop, Target: d.ID, Value: value}, choice{Text: "Изменить товар", Action: "assembly_edit_product", Workshop: workshop, Target: d.ID, Value: value}, choice{Text: "Изменить количество", Action: "assembly_edit_quantity", Workshop: workshop, Target: d.ID, Value: value}, choice{Text: "Изменить исполнителя", Action: "assembly_edit_executor", Workshop: workshop, Target: d.ID, Value: value}, choice{Text: "Отмена", Action: "assembly_cancel", Workshop: workshop, Target: d.ID, Value: value})
}

func (b *Bot) assemblyButton(a buttonAction) error {
	if handled, err := b.assemblyExtraButton(a); handled {
		return err
	}
	d, err := b.loadAssembly(a.Key, a.Workshop)
	if err != nil {
		return err
	}
	if d == nil || d.ID != a.Target {
		return b.sendMessage(a.Key.ChatID, "Черновик устарел. Начните сборку заново.")
	}
	if assemblyRevision(a.Value) != d.Revision {
		return b.sendMessage(a.Key.ChatID, "Черновик изменился. Используйте последнее меню.")
	}
	for _, permission := range []auth.Permission{auth.TasksCreate, auth.ProductsRead} {
		if err = auth.Require(b.WS.DB(), a.Key.UserID, a.Workshop, permission); err != nil {
			return err
		}
	}
	switch a.Action {
	case "assembly_edit_executor":
		d.Assignee = 0
		return b.chooseDraftExecutor(a.Key, a.Workshop, d)
	case "assembly_executor":
		parts := strings.SplitN(a.Value, ":", 2)
		if len(parts) != 2 {
			return auth.ErrDenied
		}
		d.Assignee, _ = strconv.ParseInt(parts[1], 10, 64)
		return b.previewAssembly(a.Key, a.Workshop, d)
	case "assembly_page":
		parts := strings.SplitN(a.Value, ":", 2)
		if len(parts) != 2 {
			return auth.ErrDenied
		}
		page, _ := strconv.Atoi(parts[1])
		return b.assemblyCatalog(a.Key, a.Workshop, d, d.Query, page)
	case "assembly_edit_product":
		d.Product = 0
		return b.assemblyCatalog(a.Key, a.Workshop, d, "", 0)
	case "assembly_edit_quantity":
		d.Stage = "quantity"
		d.Orders = 0
		d.PerOrder = 0
		if err = b.storeAssembly(a.Key, a.Workshop, d); err != nil {
			return err
		}
		return b.sendMessage(a.Key.ChatID, "Сколько единиц нужно собрать? Отмена: /setup_stop.")
	case "assembly_pick":
		parts := strings.SplitN(a.Value, ":", 2)
		if len(parts) != 2 {
			return auth.ErrDenied
		}
		id, _ := strconv.ParseInt(parts[1], 10, 64)
		for _, candidate := range d.Candidates {
			if id == candidate {
				return b.acceptAssemblyProduct(a.Key, a.Workshop, d, id)
			}
		}
		return auth.ErrDenied
	}
	if a.Action == "assembly_cancel" {
		if err = b.dropAssembly(a.Key); err != nil {
			return err
		}
		return b.sendMessage(a.Key.ChatID, "Черновик сборки отменён.")
	}
	if d.Stage != "confirm" {
		return errors.New("Сначала уточните параметры задачи")
	}
	sc := memory.Scope{UserID: a.Key.UserID, WorkshopID: a.Workshop, SessionID: d.Session}
	state := memory.TaskState{ProductID: d.Product, Quantity: float64(d.Quantity), Parameters: map[string]string{"orders": strconv.FormatInt(d.Orders, 10), "per_order": strconv.FormatInt(d.PerOrder, 10)}}
	err = b.Agent.Memory.ForUser(a.Key.UserID).ConfirmAssemblyDraft(sc, a.Key.ChatID, d.ID, state, d.Revision)
	if errors.Is(err, memory.ErrActiveTask) && d.Assignee == a.Key.UserID {
		active, e := b.Agent.Memory.ForUser(a.Key.UserID).ActiveWorking(sc)
		if e != nil {
			return e
		}
		if active != nil {
			return b.pauseConflict(a.Key, a.Workshop, active, "assembly_catalog", fmt.Sprint(d.ID))
		}
	}
	if errors.Is(err, memory.ErrActiveTask) {
		return b.screen(a.Key, "У выбранного исполнителя уже есть текущая задача. Откройте список задач или выберите другого исполнителя; существующая задача не изменена.", choice{Text: "Все задачи", Action: "orders_all", Workshop: a.Workshop}, choice{Text: "Изменить исполнителя", Action: "assembly_edit_executor", Workshop: a.Workshop, Target: d.ID, Value: fmt.Sprint(d.Revision)}, choice{Text: "Отмена", Action: "assembly_cancel", Workshop: a.Workshop, Target: d.ID, Value: fmt.Sprint(d.Revision)})
	}
	if err != nil {
		return err
	}
	tasks, err := b.Agent.Memory.ForUser(a.Key.UserID).WorkshopTasks(sc, false)
	if err != nil {
		return err
	}
	for _, task := range tasks {
		if task.CreatedByUserID == a.Key.UserID && task.AssignedToUserID == d.Assignee && task.Status == "active" {
			return b.orderCard(a.Key, a.Workshop, task.ID)
		}
	}
	return b.sendMessage(a.Key.ChatID, "Задача создана.")
}
