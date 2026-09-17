package memory

import (
	"errors"
	"math"
	"path/filepath"
	"sync"
	"testing"
	"time"
	"workshop-agent/internal/inventory"
	"workshop-agent/internal/products"
	"workshop-agent/internal/storage"
	"workshop-agent/internal/testkit"
	"workshop-agent/internal/workshops"
)

type completionFixture struct {
	m                    *Service
	sc                   Scope
	path                 string
	product, gypsum, box int64
}

func newCompletionFixture(t *testing.T) completionFixture {
	t.Helper()
	path := filepath.Join(t.TempDir(), "completion.db")
	u, w := testkit.Owner(t, path)
	ws := workshops.NewService(path)
	inv := inventory.NewService(path)
	prod := products.NewBOMService(path)
	t.Cleanup(func() { ws.Close(); inv.Close(); prod.Close() })
	m := New(ws.DB()).ForUser(u)
	p, e := prod.ForUser(u).CreateProduct(w, "Слив", "", "product", 0, 0, "")
	if e != nil {
		t.Fatal(e)
	}
	g, e := inv.ForUser(u).CreateMaterial(w, "Гипс", "raw", "kg", 5, 0, "", 0, "")
	if e != nil {
		t.Fatal(e)
	}
	b, e := inv.ForUser(u).CreateMaterial(w, "Коробка", "packaging", "pcs", 10, 0, "", 0, "")
	if e != nil {
		t.Fatal(e)
	}
	if e = prod.ForUser(u).SetBOMItem(w, p, "material", g, 0, .2, "kg", 0, ""); e != nil {
		t.Fatal(e)
	}
	if e = prod.ForUser(u).SetBOMItem(w, p, "material", b, 0, 1, "pcs", 0, ""); e != nil {
		t.Fatal(e)
	}
	sc, e := m.EnsureSession(Scope{UserID: u, WorkshopID: w}, 900001)
	if e != nil {
		t.Fatal(e)
	}
	f := TaskStateMachine{m}
	task, e := f.Create(sc, TaskState{ProductID: p, Quantity: 5})
	if e != nil {
		t.Fatal(e)
	}
	sc.TaskID = task.ID
	q := 5.
	for _, action := range []string{"set_quantity", "confirm_plan", "start_production"} {
		task, e = f.Apply(sc, TaskIntent{Action: action, Quantity: &q, Version: task.Version})
		if e != nil {
			t.Fatal(e)
		}
	}
	return completionFixture{m, sc, path, p, g, b}
}
func TestCompletionAtomicReceiptRestartAndFreshStock(t *testing.T) {
	f := newCompletionFixture(t)
	c, e := f.m.PrepareCompletion(f.sc, 900001, 3)
	if e != nil {
		t.Fatal(e)
	}
	if len(c.Plan.Materials) != 2 || c.Plan.Materials[0].Quantity != 600 {
		t.Fatal(c.Plan)
	}
	if _, e = f.m.DB.Exec("UPDATE materials SET current_stock=current_stock+1000 WHERE id=?", f.gypsum); e != nil {
		t.Fatal(e)
	}
	r, e := f.m.PostCompletion(f.sc, 900001, c.Token)
	if e != nil {
		t.Fatal(e)
	}
	if r.Materials[0].After != 5400 || r.ProductAfter != 3 {
		t.Fatal(r)
	}
	store, e := storage.New(f.path)
	if e != nil {
		t.Fatal(e)
	}
	defer store.Close()
	m := New(store.DB).ForUser(f.sc.UserID)
	again, e := m.PostCompletion(f.sc, 900001, c.Token)
	if e != nil || again.RecordID != r.RecordID {
		t.Fatal(again, e)
	}
	task, e := m.Task(f.sc)
	if e != nil || task.Status != "completed" || *task.State.ProducedQuantity != 3 {
		t.Fatal(task, e)
	}
	var records, moves int
	if e = m.DB.QueryRow("SELECT (SELECT COUNT(*) FROM production_records),(SELECT COUNT(*) FROM inventory_movements WHERE reference_type='production')").Scan(&records, &moves); e != nil || records != 1 || moves != 2 {
		t.Fatal(records, moves, e)
	}
}
func TestCompletionRollbackAndValidation(t *testing.T) {
	for _, mode := range []string{"shortage", "bom", "unit", "version", "cancel", "session", "movement", "record", "audit", "history", "paused", "permission", "workshop"} {
		t.Run(mode, func(t *testing.T) {
			f := newCompletionFixture(t)
			c, e := f.m.PrepareCompletion(f.sc, 900001, 3)
			if e != nil {
				t.Fatal(e)
			}
			q := ""
			switch mode {
			case "shortage":
				q = "UPDATE materials SET current_stock=0 WHERE name='Коробка'"
			case "bom":
				q = "UPDATE bom_items SET quantity=quantity+1"
			case "unit":
				q = "UPDATE materials SET display_unit='g' WHERE name='Гипс'"
			case "version":
				q = "UPDATE working_memory SET version=version+1"
			case "cancel":
				e = f.m.CancelCompletion(f.sc, 900001)
			case "session":
				e = f.m.EndSession(f.sc)
			case "paused":
				q = "UPDATE working_memory SET status='paused'"
			case "permission":
				q = "UPDATE workshop_members SET role='VIEWER'"
			case "workshop":
				q = "UPDATE user_workshop_context SET active_workshop_id=NULL"
			default:
				table := map[string]string{"movement": "inventory_movements", "record": "production_records", "audit": "audit_logs", "history": "task_transitions"}[mode]
				q = "CREATE TRIGGER fail_post BEFORE INSERT ON " + table + " BEGIN SELECT RAISE(ABORT,'test failure'); END"
			}
			if e != nil {
				t.Fatal(e)
			}
			if q != "" {
				if _, e = f.m.DB.Exec(q); e != nil {
					t.Fatal(e)
				}
			}
			if _, e = f.m.PostCompletion(f.sc, 900001, c.Token); e == nil {
				t.Fatal("accepted", mode)
			}
			var count int
			if e = f.m.DB.QueryRow("SELECT COUNT(*) FROM production_records").Scan(&count); e != nil || count != 0 {
				t.Fatal(count, e)
			}
			var stock float64
			if e = f.m.DB.QueryRow("SELECT current_stock FROM materials WHERE id=?", f.gypsum).Scan(&stock); e != nil || stock != 5000 {
				t.Fatal(stock, e)
			}
		})
	}
}
func TestCompletionConcurrentAndInvalidQuantity(t *testing.T) {
	f := newCompletionFixture(t)
	for _, n := range []float64{0, -1, 1.5, 1e10, math.NaN(), math.Inf(1)} {
		if _, e := f.m.PrepareCompletion(f.sc, 900001, n); e == nil {
			t.Fatal(n)
		}
	}
	c, e := f.m.PrepareCompletion(f.sc, 900001, 5)
	if e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	ch := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, e := f.m.PostCompletion(f.sc, 900001, c.Token); ch <- e }()
	}
	wg.Wait()
	close(ch)
	for e := range ch {
		if e != nil {
			t.Fatal(e)
		}
	}
	var count int
	_ = f.m.DB.QueryRow("SELECT COUNT(*) FROM production_records").Scan(&count)
	if count != 1 {
		t.Fatal(count)
	}
}

func TestCompletionNestedAggregationAndDailyOnce(t *testing.T) {
	f := newCompletionFixture(t)
	res, e := f.m.DB.Exec("INSERT INTO products(workshop_id,name,product_type,current_stock) VALUES(?,'Полуфабрикат','product',7)", f.sc.WorkshopID)
	if e != nil {
		t.Fatal(e)
	}
	child, e := res.LastInsertId()
	if e != nil {
		t.Fatal(e)
	}
	prod := products.NewBOMService(f.path)
	defer prod.Close()
	p := prod.ForUser(f.sc.UserID)
	if e = p.SetBOMItem(f.sc.WorkshopID, child, "material", f.gypsum, 0, .05, "kg", 0, ""); e != nil {
		t.Fatal(e)
	}
	if e = p.SetBOMItem(f.sc.WorkshopID, f.product, "product", 0, child, 2, "pcs", 0, ""); e != nil {
		t.Fatal(e)
	}
	if e = p.SetBOMItem(f.sc.WorkshopID, f.product, "material", f.gypsum, 0, .1, "kg", 0, ""); e != nil {
		t.Fatal(e)
	}
	c, e := f.m.PrepareCompletion(f.sc, 900001, 6)
	if e != nil {
		t.Fatal(e)
	}
	if c.Plan.Materials[0].Quantity != 2400 {
		t.Fatal(c.Plan.Materials)
	}
	if _, e = f.m.PostCompletion(f.sc, 900001, c.Token); e != nil {
		t.Fatal(e)
	}
	if _, e = f.m.PostCompletion(f.sc, 900001, c.Token); e != nil {
		t.Fatal(e)
	}
	var stock float64
	if e = f.m.DB.QueryRow("SELECT current_stock FROM products WHERE id=?", child).Scan(&stock); e != nil || stock != 7 {
		t.Fatal(stock, e)
	}
	daily, e := p.DailyProduction(f.sc.WorkshopID, time.Now())
	if e != nil || len(daily.Products) != 1 || daily.Products[0].Good != 6 {
		t.Fatal(daily, e)
	}
}
func TestCompletionBOMCycleMissingAndChanged(t *testing.T) {
	f := newCompletionFixture(t)
	c, e := f.m.PrepareCompletion(f.sc, 900001, 6)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.m.DB.Exec("UPDATE bom_items SET technical_loss_percent=10"); e != nil {
		t.Fatal(e)
	}
	if _, e = f.m.PostCompletion(f.sc, 900001, c.Token); !errors.Is(e, products.ErrProductionChanged) {
		t.Fatal(e)
	}
	if _, e = f.m.DB.Exec("DELETE FROM bom_items"); e != nil {
		t.Fatal(e)
	}
	if _, e = f.m.PrepareCompletion(f.sc, 900001, 3); !errors.Is(e, products.ErrIncompleteBOM) {
		t.Fatal(e)
	}
	if _, e = f.m.DB.Exec("INSERT INTO bom_items(workshop_id,product_id,component_type,component_product_id,quantity,unit,technical_loss_percent,notes) VALUES(?,?,'product',?,1,'pcs',0,'')", f.sc.WorkshopID, f.product, f.product); e != nil {
		t.Fatal(e)
	}
	if _, e = f.m.PrepareCompletion(f.sc, 900001, 3); e == nil {
		t.Fatal("cycle accepted")
	}
}
