package bootstrap

import (
	"fmt"
	"time"

	"workshop-agent/internal/inventory"
	"workshop-agent/internal/products"
	"workshop-agent/internal/workshops"
)

func InitProject(databasePath string) error {
	wsSvc := workshops.NewService(databasePath)
	defer wsSvc.Close()

	invSvc := inventory.NewService(databasePath)
	defer invSvc.Close()

	prodSvc := products.NewBOMService(databasePath)
	defer prodSvc.Close()

	if _, err := wsSvc.CreateWorkshop("Мастерская demo"); err != nil {
		return fmt.Errorf("create workshop: %w", err)
	}

	if _, err := invSvc.CreateMaterial(1, "гипс", "raw_material", "g", 1000, 100, "", 7, "Базовый материал для тестов"); err != nil {
		return fmt.Errorf("create material: %w", err)
	}

	if _, err := prodSvc.CreateProduct(1, "Stitch", "STITCH", "product", 0, 0, "Тестовый продукт"); err != nil {
		return fmt.Errorf("create product: %w", err)
	}

	if _, err := prodSvc.CreateProduct(1, "Набор Stitch", "KIT1", "kit", 0, 0, "Тестовый набор"); err != nil {
		return fmt.Errorf("create kit: %w", err)
	}

	if err := prodSvc.SetBOMItem(1, 2, "material", 1, 0, 230, "g", 5, "Гипс для набора"); err != nil {
		return fmt.Errorf("set bom material: %w", err)
	}

	if err := prodSvc.SetBOMItem(1, 2, "product", 0, 2, 1, "pcs", 0, "Сборка из готового продукта"); err != nil {
		return fmt.Errorf("set bom product: %w", err)
	}

	_ = time.Now()
	return nil
}
