package products

import (
	"database/sql"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"workshop-agent/internal/auth"
)

var ErrChanged = errors.New("Данные продукта изменились. Подтвердите обновлённое предложение.")
var ErrInvalidEdit = errors.New("Некорректное значение продукта: текст до 200 символов, количество от 0 до 1000000000000; относительное изменение не может быть нулевым.")

type Product struct {
	ID              int64
	Name, SKU, Type string
	Stock, Minimum  float64
}
type Edit struct {
	Field, Value string
	Relative     bool
	Expected     *string
}
type EditResult struct {
	Before, After string
	Changed       bool
}

func readProduct(q auth.Querier, w, id int64) (Product, error) {
	var p Product
	err := q.QueryRow("SELECT id,name,COALESCE(sku,''),product_type,current_stock,minimum_stock FROM products WHERE workshop_id=? AND id=?", w, id).Scan(&p.ID, &p.Name, &p.SKU, &p.Type, &p.Stock, &p.Minimum)
	if errors.Is(err, sql.ErrNoRows) {
		return p, auth.ErrDenied
	}
	return p, err
}
func (s *Service) Product(w, id int64) (Product, error) {
	if err := auth.Require(s.store.DB, s.userID, w, auth.ProductsRead); err != nil {
		return Product{}, err
	}
	return readProduct(s.store.DB, w, id)
}
func (p Product) Field(field string) string {
	switch field {
	case "name":
		return p.Name
	case "sku":
		return p.SKU
	case "product_type":
		return p.Type
	case "current_stock":
		return strconv.FormatFloat(p.Stock, 'g', -1, 64)
	case "minimum_stock":
		return strconv.FormatFloat(p.Minimum, 'g', -1, 64)
	}
	return ""
}

var decimal = regexp.MustCompile(`^[+-]?[0-9]+(?:[.,][0-9]+)?$`)

func EditValue(p Product, e Edit) (string, error) {
	value := strings.TrimSpace(e.Value)
	switch e.Field {
	case "name", "sku":
		if e.Relative || len([]rune(value)) > 200 || (e.Field == "name" && value == "") {
			return "", ErrInvalidEdit
		}
		if e.Field == "sku" && value == "-" {
			value = ""
		}
		return value, nil
	case "product_type":
		if !e.Relative && (value == "product" || value == "kit" || value == "semi_finished") {
			return value, nil
		}
		return "", ErrInvalidEdit
	case "current_stock", "minimum_stock":
		if !decimal.MatchString(value) {
			return "", ErrInvalidEdit
		}
		n, err := strconv.ParseFloat(strings.ReplaceAll(value, ",", "."), 64)
		if err != nil || math.IsNaN(n) || math.IsInf(n, 0) || math.Abs(n) > 1e12 {
			return "", ErrInvalidEdit
		}
		if e.Relative {
			if e.Field != "current_stock" || n == 0 {
				return "", ErrInvalidEdit
			}
			n += p.Stock
		}
		if n < 0 || n > 1e12 || math.IsNaN(n) || math.IsInf(n, 0) {
			return "", ErrInvalidEdit
		}
		return strconv.FormatFloat(n, 'g', -1, 64), nil
	}
	return "", ErrInvalidEdit
}

// ApplyEdit locks before reading, compares the displayed field, and commits
// product + movement + audit together. Relative edits use the locked stock.
func (s *Service) ApplyEdit(w, id int64, e Edit) (EditResult, error) {
	var out EditResult
	tx, err := s.store.DB.Begin()
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("UPDATE schema_migrations SET name=name WHERE number=100"); err != nil {
		return out, err
	}
	for _, perm := range []auth.Permission{auth.ProductsRead, auth.ProductsWrite} {
		if err = auth.Require(tx, s.userID, w, perm); err != nil {
			return out, err
		}
	}
	var active sql.NullInt64
	if err = tx.QueryRow("SELECT active_workshop_id FROM user_workshop_context WHERE user_id=?", s.userID).Scan(&active); err != nil {
		return out, err
	}
	if !active.Valid || active.Int64 != w {
		return out, auth.ErrDenied
	}
	p, err := readProduct(tx, w, id)
	if err != nil {
		return out, err
	}
	out.Before = p.Field(e.Field)
	if e.Expected != nil && *e.Expected != out.Before {
		return out, ErrChanged
	}
	out.After, err = EditValue(p, e)
	if err != nil {
		return out, err
	}
	if out.After == out.Before {
		return out, nil
	}
	// Field is restricted to the EditValue whitelist above.
	if _, err = tx.Exec("UPDATE products SET "+e.Field+"=? WHERE id=? AND workshop_id=?", out.After, id, w); err != nil {
		return out, err
	}
	if e.Field == "current_stock" {
		after, _ := strconv.ParseFloat(out.After, 64)
		if _, err = tx.Exec("INSERT INTO product_movements(workshop_id,product_id,date,quantity,movement_type,reference_type,user_id,comment) VALUES(?,?,CURRENT_TIMESTAMP,?,'manual_adjustment','manual',?,'Изменение остатка продукта')", w, id, after-p.Stock, s.userID); err != nil {
			return out, err
		}
	}
	if _, err = tx.Exec("INSERT INTO audit_logs(workshop_id,entity_type,entity_id,actor_user_id,actor_name,action,field_name,old_value,new_value,details,event_type,metadata_json) VALUES(?,'product',?,?,'','update',?,?,?,'Редактирование продукта','PRODUCT_UPDATED','{}')", w, id, s.userID, e.Field, out.Before, out.After); err != nil {
		return out, err
	}
	err = tx.Commit()
	out.Changed = err == nil
	return out, err
}

func TypeLabel(value string) string {
	switch value {
	case "product":
		return "Готовое изделие"
	case "kit":
		return "Набор"
	case "semi_finished":
		return "Полуфабрикат"
	}
	return "Неизвестный тип"
}
func FieldLabel(field string) string {
	return map[string]string{"name": "Название", "sku": "Артикул (SKU)", "product_type": "Тип продукта", "current_stock": "Остаток", "minimum_stock": "Минимальный остаток"}[field]
}
func DisplayField(field, value string) string {
	if field == "product_type" {
		return TypeLabel(value)
	}
	if field == "current_stock" || field == "minimum_stock" {
		return fmt.Sprint(value, " шт")
	}
	if value == "" {
		return "—"
	}
	return value
}
