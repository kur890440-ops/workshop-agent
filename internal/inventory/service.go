package inventory

import (
	"database/sql"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"workshop-agent/internal/auth"
	"workshop-agent/internal/storage"
)

type Service struct {
	store  *storage.Store
	userID int64
}

func NewService(path string) *Service {
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

func ConvertToBase(unit string, qty float64) float64 {
	unit = strings.ToLower(strings.TrimSpace(unit))
	switch unit {
	case "kg", "килограмм", "килограммов", "kilogram", "kilograms":
		return qty * 1000
	case "g", "гр", "грамм", "граммов", "gram", "grams":
		return qty
	case "l", "литр", "литры", "liter", "liters":
		return qty * 1000
	case "ml", "миллилитр", "миллилитры", "milliliter", "milliliters":
		return qty
	case "pcs", "шт", "штук", "штуки", "piece", "pieces", "pc":
		return qty
	default:
		return qty
	}
}

func ConvertFromBase(unit string, qty float64) float64 {
	unit = strings.ToLower(strings.TrimSpace(unit))
	switch unit {
	case "kg", "килограмм", "килограммов", "kilogram", "kilograms":
		return qty / 1000
	case "g", "гр", "грамм", "граммов", "gram", "grams":
		return qty
	case "l", "литр", "литры", "liter", "liters":
		return qty / 1000
	case "ml", "миллилитр", "миллилитры", "milliliter", "milliliters":
		return qty
	case "pcs", "шт", "штук", "штуки", "piece", "pieces", "pc":
		return qty
	default:
		return qty
	}
}

func NormalizeUnit(unit string) string {
	unit = strings.ToLower(strings.TrimSpace(unit))
	switch unit {
	case "kg", "килограмм", "килограммов", "kilogram", "kilograms":
		return "g"
	case "g", "гр", "грамм", "граммов", "gram", "grams":
		return "g"
	case "l", "литр", "литры", "liter", "liters":
		return "ml"
	case "ml", "миллилитр", "миллилитры", "milliliter", "milliliters":
		return "ml"
	case "pcs", "шт", "штук", "штуки", "piece", "pieces", "pc":
		return "pcs"
	default:
		return unit
	}
}

func (s *Service) CreateMaterial(workshopID int64, name, category, baseUnit string, currentStock, minimumStock float64, supplier string, leadTimeDays int, notes string) (int64, error) {
	if err := auth.Require(s.store.DB, s.userID, workshopID, auth.InventoryWrite); err != nil {
		return 0, err
	}
	baseUnit = CanonicalDisplayUnit(baseUnit)
	if currentStock < 0 || minimumStock < 0 || math.IsNaN(currentStock) || math.IsNaN(minimumStock) || math.IsInf(ConvertToBase(baseUnit, currentStock), 0) || math.IsInf(ConvertToBase(baseUnit, minimumStock), 0) {
		return 0, fmt.Errorf("invalid material quantity")
	}
	res, err := s.store.DB.Exec(`INSERT INTO materials (workshop_id, name, category, base_unit, display_unit, current_stock, minimum_stock, supplier, lead_time_days, notes, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, workshopID, name, category, NormalizeUnit(baseUnit), baseUnit, ConvertToBase(baseUnit, currentStock), ConvertToBase(baseUnit, minimumStock), supplier, leadTimeDays, notes, time.Now().Format(time.RFC3339), time.Now().Format(time.RFC3339))
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if err := s.LogAudit(workshopID, "material", id, s.userID, "", "create", "", "", "", fmt.Sprintf("created material %s", name)); err != nil {
		return 0, err
	}
	return id, nil
}

func (s *Service) UpdateMaterial(workshopID, materialID int64, fieldName, newValue string, actorUserID int64, actorName string) error {
	if err := auth.Require(s.store.DB, s.userID, workshopID, auth.InventoryWrite); err != nil {
		return err
	}
	var oldValue string
	var currentField string

	switch fieldName {
	case "name":
		currentField = "name"
		if err := s.store.DB.QueryRow(`SELECT name FROM materials WHERE id = ? AND workshop_id = ?`, materialID, workshopID).Scan(&oldValue); err != nil {
			return err
		}
		_, err := s.store.DB.Exec(`UPDATE materials SET name = ?, updated_at = ? WHERE id = ? AND workshop_id = ?`, newValue, time.Now().Format(time.RFC3339), materialID, workshopID)
		if err != nil {
			return err
		}
		return s.LogAudit(workshopID, "material", materialID, actorUserID, actorName, "update", currentField, oldValue, newValue, "material name changed")
	case "current_stock":
		currentField = "current_stock"
		var current float64
		if err := s.store.DB.QueryRow(`SELECT current_stock FROM materials WHERE id = ? AND workshop_id = ?`, materialID, workshopID).Scan(&current); err != nil {
			return err
		}
		oldValue = fmt.Sprintf("%v", current)
		stock, err := strconv.ParseFloat(newValue, 64)
		if err != nil {
			return fmt.Errorf("invalid numeric value for current_stock: %w", err)
		}
		_, err = s.store.DB.Exec(`UPDATE materials SET current_stock = ?, updated_at = ? WHERE id = ? AND workshop_id = ?`, stock, time.Now().Format(time.RFC3339), materialID, workshopID)
		if err != nil {
			return err
		}
		return s.LogAudit(workshopID, "material", materialID, actorUserID, actorName, "update", currentField, oldValue, newValue, "material stock changed")
	case "minimum_stock":
		currentField = "minimum_stock"
		var current float64
		if err := s.store.DB.QueryRow(`SELECT minimum_stock FROM materials WHERE id = ? AND workshop_id = ?`, materialID, workshopID).Scan(&current); err != nil {
			return err
		}
		oldValue = fmt.Sprintf("%v", current)
		minStock, err := strconv.ParseFloat(newValue, 64)
		if err != nil {
			return fmt.Errorf("invalid numeric value for minimum_stock: %w", err)
		}
		_, err = s.store.DB.Exec(`UPDATE materials SET minimum_stock = ?, updated_at = ? WHERE id = ? AND workshop_id = ?`, minStock, time.Now().Format(time.RFC3339), materialID, workshopID)
		if err != nil {
			return err
		}
		return s.LogAudit(workshopID, "material", materialID, actorUserID, actorName, "update", currentField, oldValue, newValue, "minimum stock changed")
	case "supplier":
		currentField = "supplier"
		if err := s.store.DB.QueryRow(`SELECT supplier FROM materials WHERE id = ? AND workshop_id = ?`, materialID, workshopID).Scan(&oldValue); err != nil {
			return err
		}
		_, err := s.store.DB.Exec(`UPDATE materials SET supplier = ?, updated_at = ? WHERE id = ? AND workshop_id = ?`, newValue, time.Now().Format(time.RFC3339), materialID, workshopID)
		if err != nil {
			return err
		}
		return s.LogAudit(workshopID, "material", materialID, actorUserID, actorName, "update", currentField, oldValue, newValue, "supplier changed")
	default:
		return fmt.Errorf("unsupported material field: %s", fieldName)
	}
}

func (s *Service) LogAudit(workshopID int64, entityType string, entityID int64, actorUserID int64, actorName string, action string, fieldName string, oldValue string, newValue string, details string) error {
	if err := auth.Require(s.store.DB, s.userID, workshopID, auth.InventoryWrite); err != nil {
		return err
	}
	_, err := s.store.DB.Exec(`INSERT INTO audit_logs (workshop_id, entity_type, entity_id, actor_user_id, actor_name, action, field_name, old_value, new_value, details, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, workshopID, entityType, entityID, s.userID, actorName, action, fieldName, oldValue, newValue, details, time.Now().Format(time.RFC3339))
	return err
}

func (s *Service) GetMaterialID(workshopID int64, name string) (int64, error) {
	if err := auth.Require(s.store.DB, s.userID, workshopID, auth.InventoryRead); err != nil {
		return 0, err
	}
	var id int64
	err := s.store.DB.QueryRow(`SELECT id FROM materials WHERE workshop_id = ? AND LOWER(name) = LOWER(?) LIMIT 1`, workshopID, name).Scan(&id)
	if err != nil {
		return 0, err
	}
	return id, nil
}

func (s *Service) GetMaterialStock(workshopID int64, name string) (float64, error) {
	if err := auth.Require(s.store.DB, s.userID, workshopID, auth.InventoryRead); err != nil {
		return 0, err
	}
	var stock float64
	err := s.store.DB.QueryRow(`SELECT current_stock FROM materials WHERE workshop_id = ? AND LOWER(name) = LOWER(?) LIMIT 1`, workshopID, name).Scan(&stock)
	if err != nil {
		if err == sql.ErrNoRows {
			return 0, fmt.Errorf("материал %q не найден в базе данных для workshop_id=%d", name, workshopID)
		}
		return 0, err
	}
	return stock, nil
}

func (s *Service) AdjustStock(workshopID int64, materialName string, delta float64) error {
	if err := auth.Require(s.store.DB, s.userID, workshopID, auth.InventoryWrite); err != nil {
		return err
	}
	var materialID int64
	var stock float64
	var unit string
	err := s.store.DB.QueryRow(`SELECT id, current_stock, base_unit FROM materials WHERE workshop_id = ? AND LOWER(name) = LOWER(?) LIMIT 1`, workshopID, materialName).Scan(&materialID, &stock, &unit)
	if err != nil {
		return err
	}
	newStock := stock + delta
	if newStock < 0 {
		return fmt.Errorf("negative stock for %s not allowed", materialName)
	}
	_, err = s.store.DB.Exec(`UPDATE materials SET current_stock = ?, updated_at = ? WHERE id = ? AND workshop_id = ?`, newStock, time.Now().Format(time.RFC3339), materialID, workshopID)
	if err != nil {
		return err
	}
	_, err = s.store.DB.Exec(`INSERT INTO inventory_movements (workshop_id, material_id, date, quantity, movement_type, reference_type, user_id, comment) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, workshopID, materialID, time.Now().Format(time.RFC3339), delta, "manual_adjustment", "manual", s.userID, fmt.Sprintf("Manual adjustment for %s in %s", materialName, unit))
	return err
}

func (s *Service) ListMaterials(workshopID int64) ([]map[string]any, error) {
	if err := auth.Require(s.store.DB, s.userID, workshopID, auth.InventoryRead); err != nil {
		return nil, err
	}
	rows, err := s.store.DB.Query(`SELECT id, name, category, base_unit, display_unit, current_stock, minimum_stock, supplier, lead_time_days, notes FROM materials WHERE workshop_id = ? ORDER BY name`, workshopID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id int64
		var name, category, baseUnit, displayUnit, supplier, notes string
		var currentStock, minimumStock float64
		var leadTimeDays int
		if err := rows.Scan(&id, &name, &category, &baseUnit, &displayUnit, &currentStock, &minimumStock, &supplier, &leadTimeDays, &notes); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"id": id, "name": name, "category": category, "base_unit": baseUnit, "display_unit": displayUnit, "current_stock": currentStock, "minimum_stock": minimumStock, "supplier": supplier, "lead_time_days": leadTimeDays, "notes": notes})
	}
	return out, rows.Err()
}

func (s *Service) CreateMaterialStockMovement(workshopID, materialID int64, qty float64, movementType, refType string, refID, userID int64, comment string) error {
	if err := auth.Require(s.store.DB, s.userID, workshopID, auth.InventoryWrite); err != nil {
		return err
	}
	if qty == 0 {
		return nil
	}
	var count int
	if err := s.store.DB.QueryRow(`SELECT COUNT(*) FROM materials WHERE id=? AND workshop_id=?`, materialID, workshopID).Scan(&count); err != nil {
		return err
	}
	if count != 1 {
		return auth.ErrDenied
	}
	_, err := s.store.DB.Exec(`INSERT INTO inventory_movements (workshop_id, material_id, date, quantity, movement_type, reference_type, reference_id, user_id, comment) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, workshopID, materialID, time.Now().Format(time.RFC3339), qty, movementType, refType, refID, s.userID, comment)
	return err
}

func (s *Service) MaterialNeedsPurchase(workshopID int64, materialName string) (bool, float64, error) {
	if err := auth.Require(s.store.DB, s.userID, workshopID, auth.InventoryRead); err != nil {
		return false, 0, err
	}
	var currentStock, minimumStock float64
	err := s.store.DB.QueryRow(`SELECT current_stock, minimum_stock FROM materials WHERE workshop_id = ? AND LOWER(name) = LOWER(?) LIMIT 1`, workshopID, materialName).Scan(&currentStock, &minimumStock)
	if err != nil {
		return false, 0, err
	}
	return currentStock <= minimumStock, math.Max(0, minimumStock-currentStock), nil
}

func (s *Service) MaterialPurchaseRecommendation(workshopID int64, materialName string, extraDemand float64) (float64, error) {
	if err := auth.Require(s.store.DB, s.userID, workshopID, auth.InventoryRead); err != nil {
		return 0, err
	}
	var currentStock, minimumStock float64
	err := s.store.DB.QueryRow(`SELECT current_stock, minimum_stock FROM materials WHERE workshop_id = ? AND LOWER(name) = LOWER(?) LIMIT 1`, workshopID, materialName).Scan(&currentStock, &minimumStock)
	if err != nil {
		return 0, err
	}
	needed := extraDemand + minimumStock - currentStock
	if needed < 0 {
		return 0, nil
	}
	return needed, nil
}

func (s *Service) ListPurchaseNeeds(workshopID int64) ([]map[string]any, error) {
	if err := auth.Require(s.store.DB, s.userID, workshopID, auth.InventoryRead); err != nil {
		return nil, err
	}
	rows, err := s.store.DB.Query(`
		SELECT id, name, category, base_unit, display_unit, current_stock, minimum_stock, supplier, lead_time_days,
		       (minimum_stock - current_stock) AS order_quantity
		FROM materials
		WHERE workshop_id = ? AND current_stock < minimum_stock
		ORDER BY order_quantity DESC, name`, workshopID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	needs := make([]map[string]any, 0)
	for rows.Next() {
		var id int64
		var name, category, baseUnit, displayUnit, supplier string
		var currentStock, minimumStock, orderQuantity float64
		var leadTimeDays int
		if err := rows.Scan(&id, &name, &category, &baseUnit, &displayUnit, &currentStock, &minimumStock, &supplier, &leadTimeDays, &orderQuantity); err != nil {
			return nil, err
		}
		needs = append(needs, map[string]any{
			"id":             id,
			"name":           name,
			"category":       category,
			"base_unit":      baseUnit,
			"display_unit":   displayUnit,
			"current_stock":  currentStock,
			"minimum_stock":  minimumStock,
			"order_quantity": orderQuantity,
			"supplier":       supplier,
			"lead_time_days": leadTimeDays,
		})
	}
	return needs, rows.Err()
}

// ForUser binds an internal user ID; authorization is rechecked on every operation.
func (s *Service) ForUser(userID int64) *Service { bound := *s; bound.userID = userID; return &bound }
