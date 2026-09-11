package main

import (
	"fmt"
	"log"

	"workshop-agent/internal/config"
	"workshop-agent/internal/storage"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}
	if err := storage.InitDatabase(cfg.DatabasePath); err != nil {
		log.Fatalf("инициализация базы данных не выполнена: %v", err)
	}
	fmt.Printf("База данных инициализирована: %s\n", cfg.DatabasePath)
	fmt.Println("Созданы или проверены все таблицы, существующие данные не удалялись.")
}