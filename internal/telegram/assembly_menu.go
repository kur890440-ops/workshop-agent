package telegram

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"workshop-agent/internal/auth"
	"workshop-agent/internal/memory"
)

func (b *Bot) beginAssembly(key sessionKey, w, quantity, orders int64, query string) error {
	for _, p := range []auth.Permission{auth.TasksCreate, auth.ProductsRead} {
		if err := auth.Require(b.WS.DB(), key.UserID, w, p); err != nil {
			return err
		}
	}
	sc, err := b.Agent.Memory.ForUser(key.UserID).EnsureSession(memory.Scope{UserID: key.UserID, WorkshopID: w}, key.ChatID)
	if err != nil {
		return err
	}
	d := &assemblyDraft{Session: sc.SessionID, Quantity: quantity, Orders: orders, Stage: "product", Query: query}
	if err = b.storeAssembly(key, w, d); err != nil {
		return err
	}
	active, err := b.Agent.Memory.ForUser(key.UserID).ActiveWorking(sc)
	if err != nil {
		return err
	}
	members, e := b.Agent.Memory.ForUser(key.UserID).Executors(sc)
	if e != nil {
		return e
	}
	if active != nil && (len(members) == 1 || auth.Require(b.WS.DB(), key.UserID, w, auth.TasksAssign) != nil) {
		return b.pauseConflict(key, w, active, "assembly_catalog", strconv.FormatInt(d.ID, 10))
	}
	return b.assemblyCatalog(key, w, d, query, 0)
}

type pauseContinuation struct {
	Task        taskButtonData
	Next, Value string
}

func (b *Bot) pauseConflict(key sessionKey, w int64, t *memory.Task, next, value string) error {
	raw, _ := json.Marshal(pauseContinuation{Task: taskButtonData{ID: t.ID, Version: t.Version, Action: "pause"}, Next: next, Value: value})
	return b.screen(key, fmt.Sprintf("У вас уже есть активная задача: %g шт. %s.\nОткройте /task; отмена задачи: /task cancel. Пауза — только по вашему выбору.", t.State.Quantity, t.State.ProductName), choice{Text: "Открыть текущую", Action: "orders_open", Workshop: w, Value: t.ID}, choice{Text: "Поставить текущую на паузу и продолжить", Action: "assembly_pause_existing", Workshop: w, Value: string(raw)}, choice{Text: "Отмена", Action: "assembly_abort", Workshop: w})
}
func (b *Bot) assemblyCatalog(key sessionKey, w int64, d *assemblyDraft, query string, page int) error {
	items, err := b.Prod.ForUser(key.UserID).ListProducts(w)
	if err != nil {
		return err
	}
	ids := []int64{}
	names := map[int64]string{}
	for _, p := range items {
		names[p["id"].(int64)] = fmt.Sprint(p["name"])
		if query == "" || productNameMatches(fmt.Sprint(p["name"]), query) {
			ids = append(ids, p["id"].(int64))
		}
	}
	heading := "Создать задачу сборки? Выберите товар."
	if query != "" && len(ids) > 1 {
		heading = "Название неоднозначно. Выберите подходящий товар."
	}
	if query != "" && len(ids) == 0 {
		heading = "Подходящий товар не найден. Выберите из каталога."
		for _, p := range items {
			ids = append(ids, p["id"].(int64))
		}
	}
	if len(ids) == 1 {
		heading = "Подойдёт «" + names[ids[0]] + "»?"
	}
	d.Candidates = ids
	d.Query = query
	d.Stage = "product"
	if err = b.storeAssembly(key, w, d); err != nil {
		return err
	}
	if page < 0 || page*8 >= len(ids) {
		page = 0
	}
	end := page*8 + 8
	if end > len(ids) {
		end = len(ids)
	}
	choices := []choice{}
	for i := page * 8; i < end; i++ {
		heading += fmt.Sprintf("\n%d. %s", i+1, materialLabel(names[ids[i]]))
		label := fmt.Sprintf("%d. %s", i+1, materialLabel(names[ids[i]]))
		if len(ids) == 1 {
			label = "Выбрать"
		}
		choices = append(choices, choice{Text: label, Action: "assembly_pick", Workshop: w, Target: d.ID, Value: fmt.Sprintf("%d:%d", d.Revision, ids[i])})
	}
	if len(ids) == 0 {
		heading = "В мастерской пока нет товаров. Сначала добавьте товар."
		if auth.Require(b.WS.DB(), key.UserID, w, auth.ProductsWrite) == nil {
			choices = append(choices, choice{Text: "Добавить продукт", Action: "product_add", Workshop: w})
		}
	}
	if page > 0 {
		choices = append(choices, choice{Text: "Назад", Action: "assembly_page", Workshop: w, Target: d.ID, Value: fmt.Sprintf("%d:%d", d.Revision, page-1)})
	}
	if end < len(ids) {
		choices = append(choices, choice{Text: "Далее", Action: "assembly_page", Workshop: w, Target: d.ID, Value: fmt.Sprintf("%d:%d", d.Revision, page+1)})
	}
	choices = append(choices, choice{Text: "Другой товар", Action: "assembly_edit_product", Workshop: w, Target: d.ID, Value: fmt.Sprint(d.Revision)}, choice{Text: "Отмена", Action: "assembly_cancel", Workshop: w, Target: d.ID, Value: fmt.Sprint(d.Revision)})
	b.rememberMaterialList(key, w, ids)
	b.uiMu.Lock()
	snap := b.materialLists[key]
	snap.Kind = "product"
	b.materialLists[key] = snap
	b.uiMu.Unlock()
	return b.screen(key, heading, choices...)
}
func (b *Bot) acceptAssemblyProduct(key sessionKey, w int64, d *assemblyDraft, id int64) error {
	p, err := b.Prod.ForUser(key.UserID).Product(w, id)
	if err != nil {
		return err
	}
	d.Product = id
	if d.Quantity == 0 || d.Orders > 0 && d.PerOrder == 0 {
		d.Stage = "quantity"
		if err = b.storeAssembly(key, w, d); err != nil {
			return err
		}
		question := "Сколько единиц нужно собрать?"
		if d.Orders > 0 {
			question = "Сколько штук входит в один заказ?"
		}
		return b.sendMessage(key.ChatID, p.Name+". "+question+" Отмена: /setup_stop.")
	}
	return b.previewAssembly(key, w, d)
}
func (b *Bot) assemblyExtraButton(a buttonAction) (bool, error) {
	if a.Action == "assembly_abort" {
		if err := b.dropAssembly(a.Key); err != nil {
			return true, err
		}
		return true, b.sendMessage(a.Key.ChatID, "Черновик отменён. Задача не создана.")
	}
	if a.Action == "assembly_pause_existing" {
		var v pauseContinuation
		if err := json.Unmarshal([]byte(a.Value), &v); err != nil {
			return true, err
		}
		var d *assemblyDraft
		if v.Next == "assembly_catalog" {
			var err error
			d, err = b.loadAssembly(a.Key, a.Workshop)
			if err != nil {
				return true, err
			}
			if d == nil || fmt.Sprint(d.ID) != v.Value {
				return true, memory.ErrTaskChanged
			}
		}
		_, err := (memory.TaskStateMachine{Memory: b.Agent.Memory.ForUser(a.Key.UserID)}).Apply(memory.Scope{UserID: a.Key.UserID, WorkshopID: a.Workshop, TaskID: v.Task.ID}, memory.TaskIntent{Action: "pause", Version: v.Task.Version})
		if err != nil {
			return true, err
		}
		if v.Next == "orders_open" {
			return true, b.orderCard(a.Key, a.Workshop, v.Value)
		}
		return true, b.assemblyCatalog(a.Key, a.Workshop, d, d.Query, 0)
	}
	return false, nil
}
func assemblyRevision(value string) int {
	n, _ := strconv.Atoi(strings.SplitN(value, ":", 2)[0])
	return n
}
