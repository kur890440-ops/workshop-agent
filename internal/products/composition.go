package products

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"workshop-agent/internal/auth"
	"workshop-agent/internal/inventory"
)

var ErrBOMQuantity = errors.New("Введите положительное количество без знака, не более 1000000000000. Допустимы точка и запятая.")
var ErrBOMDuplicate = errors.New("Компонент уже входит в состав. Выберите изменение количества существующей строки.")

type BOMRow struct {
	ID, MaterialID, ProductID            int64
	Kind, Name, Unit, DisplayUnit, Notes string
	Quantity, Loss                       float64
}
type Composition struct {
	Rows    []BOMRow
	Version string
}
type BOMChange struct {
	Action                       string
	RowID, MaterialID            int64
	Value, DisplayUnit, Expected string
}
type bomQuerier interface {
	auth.Querier
	Query(string, ...any) (*sql.Rows, error)
}

func composition(q bomQuerier, w, p int64) (Composition, error) {
	out := Composition{Rows: []BOMRow{}}
	if _, err := readProduct(q, w, p); err != nil {
		return out, err
	}
	rows, err := q.Query(`SELECT b.id,b.component_type,COALESCE(b.material_id,0),COALESCE(b.component_product_id,0),b.quantity,b.unit,b.technical_loss_percent,COALESCE(b.notes,''),CASE WHEN b.component_type='material' THEN m.name ELSE p.name END,CASE WHEN b.component_type='material' THEN COALESCE(NULLIF(m.display_unit,''),m.base_unit) ELSE 'pcs' END FROM bom_items b LEFT JOIN materials m ON b.material_id=m.id AND b.workshop_id=m.workshop_id LEFT JOIN products p ON b.component_product_id=p.id AND b.workshop_id=p.workshop_id WHERE b.workshop_id=? AND b.product_id=? ORDER BY b.id`, w, p)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var r BOMRow
		var name, unit sql.NullString
		if err = rows.Scan(&r.ID, &r.Kind, &r.MaterialID, &r.ProductID, &r.Quantity, &r.Unit, &r.Loss, &r.Notes, &name, &unit); err != nil {
			return out, err
		}
		if !name.Valid || !unit.Valid {
			return out, auth.ErrDenied
		}
		r.Name = name.String
		r.DisplayUnit = unit.String
		out.Rows = append(out.Rows, r)
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	raw, err := json.Marshal(out.Rows)
	if err != nil {
		return out, err
	}
	out.Version = fmt.Sprintf("%x", sha256.Sum256(raw))
	return out, nil
}
func (s *Service) Composition(w, p int64) (Composition, error) {
	for _, perm := range []auth.Permission{auth.ProductsRead, auth.BOMRead, auth.InventoryRead} {
		if err := auth.Require(s.store.DB, s.userID, w, perm); err != nil {
			return Composition{}, err
		}
	}
	return composition(s.store.DB, w, p)
}
func (r BOMRow) DisplayQuantity() string {
	if inventory.NormalizeUnit(r.Unit) != inventory.NormalizeUnit(r.DisplayUnit) {
		return fmt.Sprintf("%g %s (проверьте единицу BOM)", r.Quantity, r.Unit)
	}
	return inventory.FormatQuantity(inventory.ConvertToBase(r.Unit, r.Quantity), r.DisplayUnit)
}

var positiveDecimal = regexp.MustCompile(`^[0-9]+(?:[.,][0-9]+)?$`)

func BOMQuantity(text, display, stored string) (float64, error) {
	text = strings.TrimSpace(text)
	if !positiveDecimal.MatchString(text) {
		return 0, ErrBOMQuantity
	}
	v, err := strconv.ParseFloat(strings.ReplaceAll(text, ",", "."), 64)
	if err != nil || v <= 0 || v > 1e12 || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, ErrBOMQuantity
	}
	if inventory.NormalizeUnit(display) != inventory.NormalizeUnit(stored) {
		return 0, ErrIncompleteBOM
	}
	v = inventory.ConvertToBase(display, v) / inventory.ConvertToBase(stored, 1)
	if v <= 0 || math.IsNaN(v) || math.IsInf(v, 0) || v > 1e12 {
		return 0, ErrBOMQuantity
	}
	return v, nil
}
func (s *Service) ApplyBOMChange(w, p int64, c BOMChange) (bool, error) {
	tx, err := s.store.DB.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("UPDATE schema_migrations SET name=name WHERE number=100"); err != nil {
		return false, err
	}
	for _, perm := range []auth.Permission{auth.ProductsRead, auth.BOMRead, auth.BOMWrite, auth.InventoryRead} {
		if err = auth.Require(tx, s.userID, w, perm); err != nil {
			return false, err
		}
	}
	var active sql.NullInt64
	if err = tx.QueryRow("SELECT active_workshop_id FROM user_workshop_context WHERE user_id=?", s.userID).Scan(&active); err != nil {
		return false, err
	}
	if !active.Valid || active.Int64 != w {
		return false, auth.ErrDenied
	}
	current, err := composition(tx, w, p)
	if err != nil {
		return false, err
	}
	var before, after *BOMRow
	if c.Action == "add" {
		r := BOMRow{Kind: "material", MaterialID: c.MaterialID}
		if err = tx.QueryRow("SELECT name,base_unit,COALESCE(NULLIF(display_unit,''),base_unit) FROM materials WHERE workshop_id=? AND id=?", w, c.MaterialID).Scan(&r.Name, &r.Unit, &r.DisplayUnit); err != nil {
			return false, auth.ErrDenied
		}
		after = &r
	} else {
		for _, r := range current.Rows {
			if r.ID == c.RowID {
				copy := r
				before = &copy
				next := r
				after = &next
				break
			}
		}
		if before == nil {
			return false, ErrChanged
		}
	}
	if c.Action != "remove" && after.DisplayUnit != c.DisplayUnit {
		return false, inventory.ErrUnitChanged
	}
	if c.Expected == "" || c.Expected != current.Version {
		return false, ErrChanged
	}
	switch c.Action {
	case "add":
		for _, r := range current.Rows {
			if r.Kind == "material" && r.MaterialID == c.MaterialID {
				return false, ErrBOMDuplicate
			}
		}
	case "update", "remove":
	default:
		return false, ErrInvalidEdit
	}
	if c.Action != "remove" {
		after.Quantity, err = BOMQuantity(c.Value, c.DisplayUnit, after.Unit)
		if err != nil {
			return false, err
		}
	}
	if c.Action == "update" && after.Quantity == before.Quantity {
		return false, nil
	}
	id := c.RowID
	switch c.Action {
	case "add":
		r, e := tx.Exec("INSERT INTO bom_items(workshop_id,product_id,component_type,material_id,quantity,unit,notes) VALUES(?,?,'material',?,?,?,'')", w, p, c.MaterialID, after.Quantity, after.Unit)
		if e != nil {
			return false, e
		}
		id, err = r.LastInsertId()
		after.ID = id
	case "update":
		_, err = tx.Exec("UPDATE bom_items SET quantity=? WHERE workshop_id=? AND product_id=? AND id=?", after.Quantity, w, p, id)
	case "remove":
		_, err = tx.Exec("DELETE FROM bom_items WHERE workshop_id=? AND product_id=? AND id=?", w, p, id)
		after = nil
	}
	if err != nil {
		return false, err
	}
	oldRaw, _ := json.Marshal(before)
	newRaw, _ := json.Marshal(after)
	meta, _ := json.Marshal(map[string]any{"product_id": p, "bom_item_id": id, "operation": c.Action})
	if _, err = tx.Exec("INSERT INTO audit_logs(workshop_id,entity_type,entity_id,actor_user_id,actor_name,action,field_name,old_value,new_value,details,event_type,metadata_json) VALUES(?,'bom',?,?,'',?,'composition',?,?,'Изменение состава товара','BOM_COMPOSITION_CHANGED',?)", w, id, s.userID, c.Action, string(oldRaw), string(newRaw), string(meta)); err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}
