package products

import (
	"errors"
	"fmt"
	"math"
	"workshop-agent/internal/auth"
	"workshop-agent/internal/inventory"
)

var ErrIncompleteBOM = errors.New("BOM отсутствует или недостаточен для расчёта")

// SetBOMQuantity is a domain mutation, never a memory write.
func (s *Service) SetBOMQuantity(workshop, product, material int64, quantity float64) error {
	if math.IsNaN(quantity) || math.IsInf(quantity, 0) || quantity <= 0 {
		return fmt.Errorf("invalid BOM quantity")
	}
	tx, err := s.store.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE schema_migrations SET name=name WHERE number=100`); err != nil {
		return err
	}
	if err := auth.Require(tx, s.userID, workshop, auth.BOMWrite); err != nil {
		return err
	}
	var count int
	var unit string
	if err := tx.QueryRow(`SELECT COUNT(*) FROM products WHERE workshop_id=? AND id=?`, workshop, product).Scan(&count); err != nil {
		return err
	}
	if count != 1 {
		return auth.ErrDenied
	}
	if err := tx.QueryRow(`SELECT base_unit FROM materials WHERE workshop_id=? AND id=?`, workshop, material).Scan(&unit); err != nil {
		return auth.ErrDenied
	}
	if err := tx.QueryRow(`SELECT COUNT(*) FROM bom_items WHERE workshop_id=? AND product_id=? AND material_id=? AND component_type='material'`, workshop, product, material).Scan(&count); err != nil {
		return err
	}
	if count > 1 {
		return fmt.Errorf("BOM содержит несколько строк компонента; сначала уточните спецификацию")
	}
	old := 0.0
	if count == 1 {
		if err := tx.QueryRow(`SELECT quantity FROM bom_items WHERE workshop_id=? AND product_id=? AND material_id=? AND component_type='material'`, workshop, product, material).Scan(&old); err != nil {
			return err
		}
		_, err = tx.Exec(`UPDATE bom_items SET quantity=? WHERE workshop_id=? AND product_id=? AND material_id=? AND component_type='material'`, quantity, workshop, product, material)
	} else {
		_, err = tx.Exec(`INSERT INTO bom_items(workshop_id,product_id,component_type,material_id,quantity,unit,notes) VALUES(?,?,'material',?,?,?,'')`, workshop, product, material, quantity, unit)
	}
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO audit_logs(workshop_id,entity_type,entity_id,actor_user_id,actor_name,action,field_name,old_value,new_value,details,event_type,metadata_json) VALUES(?,'bom',?,?,'','BOM_QUANTITY_CHANGED','quantity',?,?,'Explicit confirmation','BOM_QUANTITY_CHANGED',?)`, workshop, product, s.userID, fmt.Sprint(old), fmt.Sprint(quantity), fmt.Sprintf(`{"product_id":%d,"material_id":%d,"source":"confirmed_domain_operation"}`, product, material))
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Service) Requirements(workshop, product int64, quantity float64) (map[int64]float64, error) {
	if quantity <= 0 || math.IsNaN(quantity) || math.IsInf(quantity, 0) {
		return nil, fmt.Errorf("invalid quantity")
	}
	if err := auth.Require(s.store.DB, s.userID, workshop, auth.BOMRead); err != nil {
		return nil, err
	}
	result := map[int64]float64{}
	path := map[int64]bool{}
	var walk func(int64, float64) error
	walk = func(id int64, q float64) error {
		var exists int
		if err := s.store.DB.QueryRow("SELECT COUNT(*) FROM products WHERE id=? AND workshop_id=?", id, workshop).Scan(&exists); err != nil {
			return err
		}
		if exists != 1 {
			return auth.ErrDenied
		}
		if path[id] {
			return fmt.Errorf("%w: cycle for product %d", ErrIncompleteBOM, id)
		}
		path[id] = true
		defer delete(path, id)
		rows, err := s.store.DB.Query(`SELECT component_type,COALESCE(material_id,0),COALESCE(component_product_id,0),quantity,technical_loss_percent,unit FROM bom_items WHERE workshop_id=? AND product_id=?`, workshop, id)
		if err != nil {
			return err
		}
		type component struct {
			kind              string
			material, product int64
			quantity          float64
			loss              float64
			unit              string
		}
		items := []component{}
		for rows.Next() {
			var c component
			if err := rows.Scan(&c.kind, &c.material, &c.product, &c.quantity, &c.loss, &c.unit); err != nil {
				rows.Close()
				return err
			}
			items = append(items, c)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if len(items) == 0 {
			return ErrIncompleteBOM
		}
		for _, c := range items {
			required := q * c.quantity * (1 + c.loss/100)
			if c.quantity <= 0 || c.loss < 0 || math.IsNaN(required) || math.IsInf(required, 0) || required <= 0 {
				return ErrIncompleteBOM
			}
			if c.kind == "material" {
				var base string
				if err := s.store.DB.QueryRow("SELECT base_unit FROM materials WHERE id=? AND workshop_id=?", c.material, workshop).Scan(&base); err != nil {
					return auth.ErrDenied
				}
				if inventory.NormalizeUnit(c.unit) != base {
					return ErrIncompleteBOM
				}
				required = inventory.ConvertToBase(c.unit, required)
				var exists int
				if err := s.store.DB.QueryRow("SELECT COUNT(*) FROM materials WHERE id=? AND workshop_id=?", c.material, workshop).Scan(&exists); err != nil {
					return err
				}
				if exists != 1 {
					return auth.ErrDenied
				}
				result[c.material] += required
				if math.IsInf(result[c.material], 0) {
					return ErrIncompleteBOM
				}
			} else {
				if c.kind != "product" || inventory.NormalizeUnit(c.unit) != "pcs" {
					return ErrIncompleteBOM
				}
				if err := walk(c.product, required); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(product, quantity); err != nil {
		return nil, err
	}
	return result, nil
}
