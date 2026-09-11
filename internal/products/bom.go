package products

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	"workshop-agent/internal/storage"
)

type Service struct {
	store *storage.Store
}

func NewBOMService(path string) *Service {
	store, err := storage.New(path)
	if err != nil {
		panic(err)
	}
	return &Service{store: store}
}

func (s *Service) Init() error {
	return s.store.Migrate()
}

func (s *Service) Close() error {
	return s.store.Close()
}

func (s *Service) DB() *sql.DB {
	return s.store.DB
}

func (s *Service) CreateProduct(workshopID int64, name, sku, productType string, currentStock, minimumStock float64, notes string) (int64, error) {
	res, err := s.store.DB.Exec(`INSERT INTO products (workshop_id, name, sku, product_type, current_stock, minimum_stock, notes, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, workshopID, name, sku, productType, currentStock, minimumStock, notes, time.Now().Format(time.RFC3339))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Service) UpdateProduct(workshopID, productID int64, fieldName, newValue string, actorUserID int64, actorName string) error {
	var oldValue string
	var currentField string

	switch fieldName {
	case "name":
		currentField = "name"
		if err := s.store.DB.QueryRow(`SELECT name FROM products WHERE id = ? AND workshop_id = ?`, productID, workshopID).Scan(&oldValue); err != nil {
			return err
		}
		_, err := s.store.DB.Exec(`UPDATE products SET name = ? WHERE id = ? AND workshop_id = ?`, newValue, productID, workshopID)
		if err != nil {
			return err
		}
		_, err = s.store.DB.Exec(`INSERT INTO audit_logs (workshop_id, entity_type, entity_id, actor_user_id, actor_name, action, field_name, old_value, new_value, details, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, workshopID, "product", productID, actorUserID, actorName, "update", currentField, oldValue, newValue, "product name changed", time.Now().Format(time.RFC3339))
		return err
	case "current_stock":
		currentField = "current_stock"
		var current float64
		if err := s.store.DB.QueryRow(`SELECT current_stock FROM products WHERE id = ? AND workshop_id = ?`, productID, workshopID).Scan(&current); err != nil {
			return err
		}
		oldValue = fmt.Sprintf("%v", current)
		stock, err := strconv.ParseFloat(newValue, 64)
		if err != nil {
			return fmt.Errorf("invalid numeric value for current_stock: %w", err)
		}
		_, err = s.store.DB.Exec(`UPDATE products SET current_stock = ? WHERE id = ? AND workshop_id = ?`, stock, productID, workshopID)
		if err != nil {
			return err
		}
		_, err = s.store.DB.Exec(`INSERT INTO audit_logs (workshop_id, entity_type, entity_id, actor_user_id, actor_name, action, field_name, old_value, new_value, details, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, workshopID, "product", productID, actorUserID, actorName, "update", currentField, oldValue, newValue, "product stock changed", time.Now().Format(time.RFC3339))
		return err
	case "minimum_stock":
		currentField = "minimum_stock"
		var current float64
		if err := s.store.DB.QueryRow(`SELECT minimum_stock FROM products WHERE id = ? AND workshop_id = ?`, productID, workshopID).Scan(&current); err != nil {
			return err
		}
		oldValue = fmt.Sprintf("%v", current)
		minStock, err := strconv.ParseFloat(newValue, 64)
		if err != nil {
			return fmt.Errorf("invalid numeric value for minimum_stock: %w", err)
		}
		_, err = s.store.DB.Exec(`UPDATE products SET minimum_stock = ? WHERE id = ? AND workshop_id = ?`, minStock, productID, workshopID)
		if err != nil {
			return err
		}
		_, err = s.store.DB.Exec(`INSERT INTO audit_logs (workshop_id, entity_type, entity_id, actor_user_id, actor_name, action, field_name, old_value, new_value, details, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, workshopID, "product", productID, actorUserID, actorName, "update", currentField, oldValue, newValue, "minimum stock changed", time.Now().Format(time.RFC3339))
		return err
	default:
		return fmt.Errorf("unsupported product field: %s", fieldName)
	}
}

func (s *Service) GetProductByName(workshopID int64, name string) (int64, error) {
	var id int64
	err := s.store.DB.QueryRow(`SELECT id FROM products WHERE workshop_id = ? AND name = ? LIMIT 1`, workshopID, name).Scan(&id)
	if err != nil {
		return 0, err
	}
	return id, nil
}

func (s *Service) SetBOMItem(workshopID, productID int64, componentType string, materialID, componentProductID int64, qty float64, unit string, technicalLossPercent float64, notes string) error {
	if componentType != "material" && componentType != "product" {
		return fmt.Errorf("invalid component_type %s", componentType)
	}
	_, err := s.store.DB.Exec(`INSERT INTO bom_items (workshop_id, product_id, component_type, material_id, component_product_id, quantity, unit, technical_loss_percent, notes) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, workshopID, productID, componentType, materialID, componentProductID, qty, strings.TrimSpace(unit), technicalLossPercent, notes)
	return err
}

func (s *Service) GetBOM(workshopID, productID int64) ([]map[string]any, error) {
	rows, err := s.store.DB.Query(`SELECT id, component_type, material_id, component_product_id, quantity, unit, technical_loss_percent, notes FROM bom_items WHERE workshop_id = ? AND product_id = ?`, workshopID, productID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id int64
		var componentType string
		var materialID, componentProductID sql.NullInt64
		var quantity float64
		var unit string
		var technicalLossPercent float64
		var notes string
		if err := rows.Scan(&id, &componentType, &materialID, &componentProductID, &quantity, &unit, &technicalLossPercent, &notes); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"id": id, "component_type": componentType, "material_id": materialID, "component_product_id": componentProductID, "quantity": quantity, "unit": unit, "technical_loss_percent": technicalLossPercent, "notes": notes})
	}
	return out, rows.Err()
}

func CalculateRequirementForProduct(workshopID int64, productID int64, quantity float64, svc *Service) []map[string]any {
	items, err := svc.GetBOM(workshopID, productID)
	if err != nil {
		return nil
	}
	out := []map[string]any{}
	for _, item := range items {
		qty := item["quantity"].(float64) * quantity
		if item["component_type"] == "material" {
			if materialID, ok := item["material_id"].(sql.NullInt64); ok && materialID.Valid {
				out = append(out, map[string]any{"material_id": materialID.Int64, "quantity": qty, "unit": item["unit"]})
			}
			continue
		}
		if item["component_type"] == "product" {
			componentProductID, ok := item["component_product_id"].(sql.NullInt64)
			if !ok || !componentProductID.Valid {
				continue
			}
			requirements := CalculateRequirementForProduct(workshopID, componentProductID.Int64, qty, svc)
			out = append(out, requirements...)
		}
	}
	return out
}

func (s *Service) ListProducts(workshopID int64) ([]map[string]any, error) {
	rows, err := s.store.DB.Query(`SELECT id, name, sku, product_type, current_stock, minimum_stock, notes FROM products WHERE workshop_id = ? ORDER BY name`, workshopID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id int64
		var name, sku, productType, notes string
		var currentStock, minimumStock float64
		if err := rows.Scan(&id, &name, &sku, &productType, &currentStock, &minimumStock, &notes); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"id": id, "name": name, "sku": sku, "product_type": productType, "current_stock": currentStock, "minimum_stock": minimumStock, "notes": notes})
	}
	return out, rows.Err()
}
