package telegram

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"workshop-agent/internal/inventory"
	"workshop-agent/internal/llm"
	"workshop-agent/internal/memory"
)

type materialListContext struct {
	SessionID int64
	Kind      string
	LastID    int64
	Workshop  int64
	IDs       []int64
	Expires   time.Time
}

func (b *Bot) rememberMaterialList(key sessionKey, workshop int64, ids []int64) {
	var sessionID int64
	if b.Agent != nil {
		sc, err := b.Agent.Memory.ForUser(key.UserID).EnsureSession(memory.Scope{UserID: key.UserID, WorkshopID: workshop}, key.ChatID)
		if err == nil {
			sessionID = sc.SessionID
		}
	}
	b.uiMu.Lock()
	defer b.uiMu.Unlock()
	if b.materialLists == nil {
		b.materialLists = map[sessionKey]materialListContext{}
	}
	for k, v := range b.materialLists {
		if time.Now().After(v.Expires) {
			delete(b.materialLists, k)
		}
	}
	b.materialLists[key] = materialListContext{SessionID: sessionID, Workshop: workshop, IDs: append([]int64(nil), ids...), Expires: time.Now().Add(15 * time.Minute), Kind: "material"}
}
func (b *Bot) forgetMaterialList(key sessionKey) {
	b.uiMu.Lock()
	defer b.uiMu.Unlock()
	delete(b.materialLists, key)
}

var stockReference = regexp.MustCompile(`^(?:а\s+)?сколько\s+(?:осталось\s+)?(?:материала\s+)?(?:№\s*)?([0-9]+|первого|второго|третьего|четв[её]ртого|пятого|шестого|седьмого|восьмого|девятого|десятого)(?:-?го)?(?:\s+материала)?(?:\s+осталось)?[?!.]*$`)

func (b *Bot) materialReference(key sessionKey, text string) (bool, error) {
	match := stockReference.FindStringSubmatch(strings.ToLower(strings.TrimSpace(text)))
	if match == nil {
		return false, nil
	}
	n, _ := strconv.Atoi(match[1])
	if n == 0 {
		n = map[string]int{"первого": 1, "второго": 2, "третьего": 3, "четвертого": 4, "четвёртого": 4, "пятого": 5, "шестого": 6, "седьмого": 7, "восьмого": 8, "девятого": 9, "десятого": 10}[match[1]]
	}
	b.uiMu.Lock()
	snapshot, ok := b.materialLists[key]
	b.uiMu.Unlock()
	workshop, err := b.WS.ActiveWorkshop(key.UserID)
	if err != nil {
		return true, err
	}
	if !ok || snapshot.Workshop != workshop || time.Now().After(snapshot.Expires) {
		b.forgetMaterialList(key)
		return true, b.sendMessage(key.ChatID, "Не вижу актуального списка материалов для этого диалога. Откройте /materials и повторите вопрос по номеру.")
	}
	if n < 1 || n > len(snapshot.IDs) {
		return true, b.sendMessage(key.ChatID, "Такого номера нет в показанном списке материалов. Укажите номер из списка или откройте /materials.")
	}
	if b.Agent != nil {
		return true, b.executeSemantic(key, workshop, &llm.StructuredCommand{Action: "get_material_stock", Reference: &llm.EntityReference{Kind: "list_position", EntityType: "material", Position: n}}, nil, "local", text)
	}
	item, err := b.Inv.ForUser(key.UserID).Material(workshop, snapshot.IDs[n-1])
	if err != nil {
		return true, err
	}
	return true, b.sendMessage(key.ChatID, fmt.Sprintf("%s — %s.", item["name"], inventory.Quantity(item, "current_stock")))
}
