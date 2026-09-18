package inventory

import (
	"errors"
	"fmt"
	"math"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"workshop-agent/internal/auth"
)

var ErrUnitChanged = errors.New("material display unit changed")

func CanonicalDisplayUnit(unit string) string {
	base := NormalizeUnit(unit)
	if ConvertToBase(unit, 1) == 1000 {
		if base == "g" {
			return "kg"
		}
		if base == "ml" {
			return "l"
		}
	}
	return base
}

func CompatibleUnits(base string) []string {
	switch base {
	case "g":
		return []string{"g", "kg"}
	case "ml":
		return []string{"ml", "l"}
	case "pcs":
		return []string{"pcs"}
	}
	return []string{base}
}
func UnitLabel(unit string) string {
	switch unit {
	case "g":
		return "г"
	case "kg":
		return "кг"
	case "ml":
		return "мл"
	case "l":
		return "л"
	case "pcs":
		return "шт"
	}
	return unit
}
func DisplayUnit(item map[string]any) string {
	if unit, ok := item["display_unit"].(string); ok && unit != "" {
		return unit
	}
	return fmt.Sprint(item["base_unit"])
}
func FormatQuantity(value float64, unit string) string {
	converted := ConvertFromBase(unit, value)
	if value != 0 && converted == 0 {
		v := new(big.Float).SetFloat64(value)
		v.Quo(v, big.NewFloat(ConvertToBase(unit, 1)))
		return v.Text('g', 12) + " " + UnitLabel(unit)
	}
	return strconv.FormatFloat(converted, 'g', 12, 64) + " " + UnitLabel(unit)
}

var explicitQuantityUnit = regexp.MustCompile(`(?i)[0-9]+(?:[.,][0-9]+)?\s*(килограмм(?:а|ов)?|грамм(?:а|ов)?|миллилитр(?:а|ов)?|литр(?:а|ов)?|штук(?:а|и)?|kg|ml|pcs|кг|мл|шт|g|l|г|л)(?:[^\p{L}\p{N}_]|$)`)

// Explicit units in natural-language input override the material's default.
func InputUnit(text, base, display string) (string, error) {
	match := explicitQuantityUnit.FindStringSubmatch(text)
	if match == nil {
		return display, nil
	}
	raw := strings.ToLower(match[1])
	switch raw {
	case "штука", "штуки", "штук", "шт":
		raw = "pcs"
	case "кг", "килограмма":
		raw = "kg"
	case "г", "грамма":
		raw = "g"
	case "мл", "миллилитра", "миллилитров":
		raw = "ml"
	case "л", "литра", "литров":
		raw = "l"
	}
	unit := CanonicalDisplayUnit(raw)
	for _, u := range CompatibleUnits(base) {
		if unit == u {
			return unit, nil
		}
	}
	return "", fmt.Errorf("incompatible quantity unit")
}
func Quantity(item map[string]any, field string) string {
	return FormatQuantity(item[field].(float64), DisplayUnit(item))
}

func (s *Service) Material(workshop, id int64) (map[string]any, error) {
	items, err := s.ListMaterials(workshop)
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		if item["id"] == id {
			return item, nil
		}
	}
	return nil, fmt.Errorf("material not found")
}
func (s *Service) MaterialByName(workshop int64, name string) (map[string]any, error) {
	id, err := s.GetMaterialID(workshop, name)
	if err != nil {
		return nil, err
	}
	return s.Material(workshop, id)
}
func (s *Service) SetDisplayUnit(workshop, id int64, unit string) error {
	tx, err := s.store.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`UPDATE schema_migrations SET name=name WHERE number=102`); err != nil {
		return err
	}
	if err = auth.Require(tx, s.userID, workshop, auth.InventoryWrite); err != nil {
		return err
	}
	var base, old string
	if err = tx.QueryRow(`SELECT base_unit,COALESCE(NULLIF(display_unit,''),base_unit) FROM materials WHERE workshop_id=? AND id=?`, workshop, id).Scan(&base, &old); err != nil {
		return err
	}
	valid := false
	for _, u := range CompatibleUnits(base) {
		if u == unit {
			valid = true
		}
	}
	if !valid {
		return fmt.Errorf("incompatible material unit")
	}
	if old == unit {
		return nil
	}
	if _, err = tx.Exec(`UPDATE materials SET display_unit=?,updated_at=CURRENT_TIMESTAMP WHERE workshop_id=? AND id=?`, unit, workshop, id); err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT INTO audit_logs(workshop_id,entity_type,entity_id,actor_user_id,actor_name,action,field_name,old_value,new_value,details,event_type,metadata_json) VALUES(?,'material',?,?,'','DISPLAY_UNIT_CHANGED','display_unit',?,?,'Working unit only','DISPLAY_UNIT_CHANGED','{}')`, workshop, id, s.userID, old, unit); err != nil {
		return err
	}
	return tx.Commit()
}

// ChangeDisplayedStock verifies the form's unit while holding the write lock.
func (s *Service) ChangeDisplayedStock(workshop, id int64, value float64, absolute bool, unit string) (float64, float64, error) {
	return s.changeStockByID(workshop, id, ConvertToBase(unit, value), absolute, unit)
}

func (s *Service) SetDisplayedMinimum(workshop, id int64, value float64, expectedUnit string) error {
	value = ConvertToBase(expectedUnit, value)
	if value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return fmt.Errorf("invalid minimum")
	}
	tx, err := s.store.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`UPDATE schema_migrations SET name=name WHERE number=102`); err != nil {
		return err
	}
	if err = auth.Require(tx, s.userID, workshop, auth.InventoryWrite); err != nil {
		return err
	}
	var unit string
	var old float64
	if err = tx.QueryRow(`SELECT COALESCE(NULLIF(display_unit,''),base_unit),minimum_stock FROM materials WHERE workshop_id=? AND id=?`, workshop, id).Scan(&unit, &old); err != nil {
		return err
	}
	if unit != expectedUnit {
		return ErrUnitChanged
	}
	if _, err = tx.Exec(`UPDATE materials SET minimum_stock=?,updated_at=CURRENT_TIMESTAMP WHERE workshop_id=? AND id=?`, value, workshop, id); err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT INTO audit_logs(workshop_id,entity_type,entity_id,actor_user_id,actor_name,action,field_name,old_value,new_value,details) VALUES(?,'material',?,?,'','update','minimum_stock',?,?,'base units')`, workshop, id, s.userID, fmt.Sprint(old), fmt.Sprint(value)); err != nil {
		return err
	}
	return tx.Commit()
}
