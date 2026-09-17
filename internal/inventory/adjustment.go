package inventory

import (
	"fmt"
	"math"
	"workshop-agent/internal/auth"
)

// AdjustStockByID changes the balance and records its movement atomically.
func (s *Service) AdjustStockByID(workshopID, materialID int64, delta float64) (float64, error) {
	stock, _, err := s.changeStockByID(workshopID, materialID, delta, false)
	return stock, err
}

// SetStockByID returns the new and previous balances from the same transaction.
func (s *Service) SetStockByID(workshopID, materialID int64, value float64) (float64, float64, error) {
	return s.changeStockByID(workshopID, materialID, value, true)
}
func (s *Service) changeStockByID(workshopID, materialID int64, delta float64, absolute bool, expectedUnit ...string) (float64, float64, error) {
	if math.IsNaN(delta) || math.IsInf(delta, 0) || (!absolute && delta == 0) || (absolute && delta < 0) {
		return 0, 0, fmt.Errorf("invalid stock delta")
	}
	tx, err := s.store.DB.Begin()
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()
	// Acquire the SQLite write lock before reading permissions and balance.
	if _, err = tx.Exec(`UPDATE materials SET current_stock=current_stock WHERE id=? AND workshop_id=?`, materialID, workshopID); err != nil {
		return 0, 0, err
	}
	if err = auth.Require(tx, s.userID, workshopID, auth.InventoryWrite); err != nil {
		return 0, 0, err
	}
	var stock float64
	if len(expectedUnit) > 0 {
		var unit string
		if err = tx.QueryRow(`SELECT COALESCE(NULLIF(display_unit,''),base_unit) FROM materials WHERE workshop_id=? AND id=?`, workshopID, materialID).Scan(&unit); err != nil {
			return 0, 0, err
		}
		if unit != expectedUnit[0] {
			return 0, 0, ErrUnitChanged
		}
	}
	if err = tx.QueryRow(`SELECT current_stock FROM materials WHERE id=? AND workshop_id=?`, materialID, workshopID).Scan(&stock); err != nil {
		return 0, 0, err
	}
	previous := stock
	if absolute {
		stock = delta
		delta = stock - previous
	} else {
		stock += delta
	}
	if absolute && stock == previous {
		return stock, previous, nil
	}
	if stock < 0 || math.IsInf(stock, 0) || math.IsNaN(stock) {
		return 0, 0, fmt.Errorf("invalid resulting stock")
	}
	if _, err = tx.Exec(`UPDATE materials SET current_stock=?, updated_at=CURRENT_TIMESTAMP WHERE id=? AND workshop_id=?`, stock, materialID, workshopID); err != nil {
		return 0, 0, err
	}
	if _, err = tx.Exec(`INSERT INTO inventory_movements (workshop_id,material_id,date,quantity,movement_type,reference_type,user_id,comment) VALUES (?,?,CURRENT_TIMESTAMP,?,'manual_adjustment','manual',?,'Изменение через меню материалов')`, workshopID, materialID, delta, s.userID); err != nil {
		return 0, 0, err
	}
	if err = tx.Commit(); err != nil {
		return 0, 0, err
	}
	return stock, previous, nil
}
