package agent

import (
	"strings"
	"testing"
	"workshop-agent/internal/memory"
)

func TestAgentMaterialDisplayUnits(t *testing.T) {
	a, u, w := day11(t)
	inv := a.Inv.ForUser(u)
	id, err := inv.CreateMaterial(w, "Гипс", "raw", "kg", 10, 12, "", 0, "")
	if err != nil {
		t.Fatal(err)
	}
	answer := turn(t, a, u, w, "Сколько гипс осталось?")
	if !strings.Contains(answer, "10 кг") {
		t.Fatal(answer)
	}
	item, err := inv.Material(w, id)
	if err != nil {
		t.Fatal(err)
	}
	if formatMaterial(item) != "Гипс: 10 кг." {
		t.Fatal(formatMaterial(item))
	}
	bound := *a
	bound.Inv = inv
	answer, err = bound.formatPurchaseNeeds(w)
	if err != nil || !strings.Contains(answer, "2 кг (сейчас 10 кг, минимум 12 кг)") {
		t.Fatal(answer, err)
	}
	product, err := a.Prod.ForUser(u).CreateProduct(w, "Гипсовое изделие", "", "product", 0, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if err = a.Prod.ForUser(u).SetBOMQuantity(w, product, id, 500); err != nil {
		t.Fatal(err)
	}
	task := &memory.Task{State: memory.TaskState{ProductID: product, ProductName: "Гипсовое изделие", Quantity: 4}}
	answer = a.taskAnswer(u, w, task, nil)
	if !strings.Contains(answer, "2 кг по BOM") {
		t.Fatal(answer)
	}
	if err = inv.SetDisplayUnit(w, id, "g"); err != nil {
		t.Fatal(err)
	}
	answer = a.taskAnswer(u, w, task, nil)
	if !strings.Contains(answer, "2000 г по BOM") {
		t.Fatal(answer)
	}
}
