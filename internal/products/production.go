package products

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"sort"
	"strconv"
	"time"
	"workshop-agent/internal/auth"
	"workshop-agent/internal/inventory"
)

type Consumption struct {
	MaterialID                  int64
	Name, BaseUnit, DisplayUnit string
	Quantity, Before, After     float64
}

var ErrProductionChanged = errors.New("Состав или единицы изменились. Проверьте новый расчёт и подтвердите его заново.")

type PostingError struct{ Message string }

func (e *PostingError) Error() string { return e.Message }

type ProductionPlan struct {
	ProductID                   int64
	ProductName                 string
	Quantity                    float64
	Fingerprint                 string
	Materials                   []Consumption
	RecordID                    int64
	ProductBefore, ProductAfter float64
}

func productionMultiply(values ...float64) float64 {
	r := big.NewRat(1, 1)
	for _, v := range values {
		n, ok := new(big.Rat).SetString(strconv.FormatFloat(v, 'g', -1, 64))
		if !ok {
			return math.NaN()
		}
		r.Mul(r, n)
	}
	v, _ := r.Float64()
	return v
}
func productionAdd(a, b float64) float64 {
	x, ok := new(big.Rat).SetString(strconv.FormatFloat(a, 'g', -1, 64))
	if !ok {
		return math.NaN()
	}
	y, ok := new(big.Rat).SetString(strconv.FormatFloat(b, 'g', -1, 64))
	if !ok {
		return math.NaN()
	}
	v, _ := x.Add(x, y).Float64()
	return v
}

// CalculateProductionTx expands nested BOMs to raw materials, never also
// consuming the intermediate product. All reads use the caller's transaction.
func CalculateProductionTx(tx *sql.Tx, actor, w, p int64, quantity float64) (ProductionPlan, error) {
	out := ProductionPlan{ProductID: p, Quantity: quantity}
	for _, perm := range []auth.Permission{auth.ProductionCreate, auth.InventoryWrite, auth.BOMRead, auth.ProductsRead} {
		if e := auth.Require(tx, actor, w, perm); e != nil {
			return out, e
		}
	}
	if quantity <= 0 || quantity > 1e9 || math.IsNaN(quantity) || math.IsInf(quantity, 0) || math.Trunc(quantity) != quantity {
		return out, errors.New("Введите целое количество от 1 до 1000000000.")
	}
	if e := tx.QueryRow("SELECT name,current_stock FROM products WHERE workshop_id=? AND id=?", w, p).Scan(&out.ProductName, &out.ProductBefore); e != nil {
		return out, e
	}
	out.ProductAfter = out.ProductBefore + quantity
	materials := map[int64]*Consumption{}
	path := map[int64]bool{}
	versions := []string{}
	nodes := 0
	var walk func(int64, float64, int) error
	walk = func(product int64, factor float64, depth int) error {
		nodes++
		if path[product] || depth > 32 || nodes > 10000 {
			return &PostingError{"Циклический или слишком глубокий BOM."}
		}
		path[product] = true
		defer delete(path, product)
		c, e := composition(tx, w, product)
		if e != nil {
			return e
		}
		if len(c.Rows) == 0 {
			return ErrIncompleteBOM
		}
		versions = append(versions, fmt.Sprintf("%d:%s", product, c.Version))
		for _, r := range c.Rows {
			if r.Quantity <= 0 || r.Loss < 0 || math.IsNaN(r.Quantity) || math.IsInf(r.Quantity, 0) || math.IsNaN(r.Loss) || math.IsInf(r.Loss, 0) {
				return ErrIncompleteBOM
			}
			amount := productionMultiply(factor, r.Quantity, productionAdd(1, r.Loss/100))
			if amount <= 0 || amount > 1e15 || math.IsNaN(amount) || math.IsInf(amount, 0) {
				return ErrIncompleteBOM
			}
			if r.Kind == "product" {
				if inventory.NormalizeUnit(r.Unit) != "pcs" {
					return ErrIncompleteBOM
				}
				if e = walk(r.ProductID, amount, depth+1); e != nil {
					return e
				}
				continue
			}
			if r.Kind != "material" {
				return ErrIncompleteBOM
			}
			m := materials[r.MaterialID]
			if m == nil {
				m = &Consumption{MaterialID: r.MaterialID}
				if e = tx.QueryRow("SELECT name,base_unit,COALESCE(NULLIF(display_unit,''),base_unit),current_stock FROM materials WHERE id=? AND workshop_id=?", r.MaterialID, w).Scan(&m.Name, &m.BaseUnit, &m.DisplayUnit, &m.Before); e != nil {
					return e
				}
				materials[r.MaterialID] = m
			}
			if inventory.NormalizeUnit(r.Unit) != m.BaseUnit || inventory.NormalizeUnit(m.DisplayUnit) != m.BaseUnit {
				return ErrIncompleteBOM
			}
			m.Quantity = productionAdd(m.Quantity, productionMultiply(inventory.ConvertToBase(r.Unit, 1), amount))
			if math.IsInf(m.Quantity, 0) || math.IsNaN(m.Quantity) || m.Quantity > 1e15 {
				return ErrIncompleteBOM
			}
		}
		return nil
	}
	if e := walk(p, quantity, 0); e != nil {
		return out, e
	}
	for _, m := range materials {
		m.After = m.Before - m.Quantity
		out.Materials = append(out.Materials, *m)
	}
	sort.Slice(out.Materials, func(i, j int) bool { return out.Materials[i].MaterialID < out.Materials[j].MaterialID })
	snapshot := out
	snapshot.ProductBefore = 0
	snapshot.ProductAfter = 0
	snapshot.Materials = append([]Consumption(nil), out.Materials...)
	for i := range snapshot.Materials {
		snapshot.Materials[i].Before = 0
		snapshot.Materials[i].After = 0
	}
	raw, _ := json.Marshal(struct {
		Plan     ProductionPlan
		Versions []string
	}{snapshot, versions})
	out.Fingerprint = fmt.Sprintf("%x", sha256.Sum256(raw))
	return out, nil
}
func (p ProductionPlan) Shortage() string {
	text := ""
	for _, m := range p.Materials {
		if m.After < 0 {
			text += fmt.Sprintf("\n%s: нужно %s, доступно %s, не хватает %s.", m.Name, inventory.FormatQuantity(m.Quantity, m.DisplayUnit), inventory.FormatQuantity(m.Before, m.DisplayUnit), inventory.FormatQuantity(-m.After, m.DisplayUnit))
		}
	}
	return text
}

// PostProductionTx must be committed together with the task transition.
func PostProductionTx(tx *sql.Tx, actor, w, creator, assignee int64, task string, p ProductionPlan) (ProductionPlan, error) {
	fresh, e := CalculateProductionTx(tx, actor, w, p.ProductID, p.Quantity)
	if e != nil {
		return p, e
	}
	if fresh.Fingerprint != p.Fingerprint {
		return p, ErrProductionChanged
	}
	p = fresh
	if s := p.Shortage(); s != "" {
		return p, &PostingError{"Недостаточно материалов:" + s}
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	raw, e := json.Marshal(p)
	if e != nil {
		return p, e
	}
	res, e := tx.Exec(`INSERT INTO production_records(workshop_id,product_id,date,attempted_quantity,good_quantity,scrap_quantity,user_id,notes,task_id,composition_json,created_by_user_id,assigned_to_user_id) VALUES(?,?,?,?,?,0,?,'task completion',?,?,?,?)`, w, p.ProductID, now, p.Quantity, p.Quantity, actor, task, string(raw), creator, assignee)
	if e != nil {
		return p, e
	}
	p.RecordID, e = res.LastInsertId()
	if e != nil {
		return p, e
	}
	for _, m := range p.Materials {
		res, e = tx.Exec("UPDATE materials SET current_stock=current_stock-? WHERE workshop_id=? AND id=? AND current_stock>=?", m.Quantity, w, m.MaterialID, m.Quantity)
		if e != nil {
			return p, e
		}
		n, _ := res.RowsAffected()
		if n != 1 {
			return p, errors.New("Остаток изменился; повторите расчёт.")
		}
		if _, e = tx.Exec(`INSERT INTO inventory_movements(workshop_id,material_id,date,quantity,movement_type,reference_type,reference_id,user_id,comment) VALUES(?,?,?,?,'production','production',?,?,'task completion')`, w, m.MaterialID, now, -m.Quantity, p.RecordID, actor); e != nil {
			return p, e
		}
	}
	if _, e = tx.Exec("UPDATE products SET current_stock=current_stock+? WHERE workshop_id=? AND id=?", p.Quantity, w, p.ProductID); e != nil {
		return p, e
	}
	if _, e = tx.Exec(`INSERT INTO product_movements(workshop_id,product_id,date,quantity,movement_type,reference_type,reference_id,user_id,comment) VALUES(?,?,?,?,'production','production',?,?,'task completion')`, w, p.ProductID, now, p.Quantity, p.RecordID, actor); e != nil {
		return p, e
	}
	raw, e = json.Marshal(p)
	if e != nil {
		return p, e
	}
	_, e = tx.Exec("UPDATE production_records SET composition_json=? WHERE id=?", string(raw), p.RecordID)
	return p, e
}
