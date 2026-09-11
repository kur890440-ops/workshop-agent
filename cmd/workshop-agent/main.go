package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"workshop-agent/internal/agent"
	"workshop-agent/internal/audit"
	"workshop-agent/internal/bootstrap"
	"workshop-agent/internal/config"
	"workshop-agent/internal/inventory"
	"workshop-agent/internal/llm"
	"workshop-agent/internal/products"
	"workshop-agent/internal/storage"
	"workshop-agent/internal/telegram"
	"workshop-agent/internal/workshops"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}

	if len(os.Args) > 1 && os.Args[1] == "setup" {
		if err := storage.InitDatabase(cfg.DatabasePath); err != nil {
			log.Fatalf("инициализация базы данных не выполнена: %v", err)
		}
		fmt.Printf("База данных инициализирована: %s\n", cfg.DatabasePath)
		fmt.Println("Все таблицы созданы или уже существовали. Данные не удалялись.")
		return
	}

	if cfg.TelegramBotToken == "" {
		log.Fatal("TELEGRAM_BOT_TOKEN is required in .env")
	}
	if cfg.LLMAPIKey == "" {
		log.Fatal("LLM_API_KEY is required in .env")
	}
	if cfg.LLMBaseURL == "" {
		log.Fatal("LLM_BASE_URL is required in .env")
	}
	if cfg.LLMModel == "" {
		log.Fatal("LLM_MODEL is required in .env")
	}
	if cfg.DatabasePath == "" {
		cfg.DatabasePath = "./data/workshop.db"
	}
	if err := storage.InitDatabase(cfg.DatabasePath); err != nil {
		log.Fatalf("database initialization failed: %v", err)
	}
	if err := bootstrap.InitProject(cfg.DatabasePath); err != nil {
		log.Printf("bootstrap warning: %v", err)
	}

	wsSvc := workshops.NewService(cfg.DatabasePath)
	defer wsSvc.Close()
	invSvc := inventory.NewService(cfg.DatabasePath)
	defer invSvc.Close()
	prodSvc := products.NewBOMService(cfg.DatabasePath)
	defer prodSvc.Close()
	auditSvc := audit.NewService(cfg.DatabasePath)
	defer auditSvc.Close()

	llmClient, err := llm.NewOpenRouterClient(cfg.LLMAPIKey, cfg.LLMBaseURL, cfg.LLMModel)
	if err != nil {
		log.Fatalf("failed to initialize LLM client: %v", err)
	}

	agentSvc := agent.NewWorkshopAgent(llmClient, wsSvc, invSvc, prodSvc)
	_ = agentSvc

	bot, err := telegram.NewBot(cfg.TelegramBotToken, wsSvc, invSvc, prodSvc, auditSvc, agentSvc)
	if err != nil {
		log.Fatalf("failed to initialize Telegram bot: %v", err)
	}

	_ = context.Background()
	fmt.Println("WorkshopAgent готов. Telegram token и LLM настроены из .env.")
	bot.Start()
}
