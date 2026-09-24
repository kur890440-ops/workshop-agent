package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"

	"workshop-agent/internal/agent"
	"workshop-agent/internal/audit"
	"workshop-agent/internal/background"
	"workshop-agent/internal/bootstrap"
	"workshop-agent/internal/cli"
	"workshop-agent/internal/config"
	"workshop-agent/internal/experiment"
	"workshop-agent/internal/integrations/mcpclient"
	"workshop-agent/internal/integrations/mcpmanager"
	"workshop-agent/internal/inventory"
	"workshop-agent/internal/llm"
	"workshop-agent/internal/marketplace"
	"workshop-agent/internal/marketplace/wildberries"
	"workshop-agent/internal/products"
	"workshop-agent/internal/storage"
	"workshop-agent/internal/telegram"
	"workshop-agent/internal/workshops"
)

func main() {
	// Version and offline diagnostics run before reading configuration or opening the application DB.
	if len(os.Args) == 1 || len(os.Args) == 2 && (os.Args[1] == "--version" || os.Args[1] == "version") {
		fmt.Printf("WorkshopAgent v%s\n", version)
		if len(os.Args) == 2 {
			return
		}
	}
	if len(os.Args) == 2 && os.Args[1] == "mcp-status" {
		manager, e := mcpmanager.New(context.Background(), wildberries.New(""), nil)
		if e != nil {
			log.Fatal("MCP initialization failed")
		}
		if e = manager.Close(); e != nil {
			log.Fatal("MCP cleanup failed")
		}
		if e = mcpclient.Print(os.Stdout, manager.Client.State()); e != nil {
			log.Fatal("MCP status output failed")
		}
		return
	}
	if len(os.Args) == 2 && os.Args[1] == "day18-background-report" {
		path, e := experiment.RunDay18(context.Background())
		fmt.Println(path)
		if e != nil {
			log.Fatal("Day18 mock report failed: ", e)
		}
		return
	}
	if len(os.Args) == 2 && os.Args[1] == "day17-mcp-report" {
		path, e := experiment.RunDay17(context.Background(), "reports/day17-first-mcp-tool")
		fmt.Println(path)
		if e != nil {
			log.Fatal("Day17 mock report failed: ", e)
		}
		return
	}
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
	if cli.IsCommand(os.Args[1:]) {
		if err := cli.Run(cfg.DatabasePath, os.Args[1:], os.Stdout); err != nil {
			log.Fatal(err)
		}
		return
	}
	if len(os.Args) == 3 && os.Args[1] == "day11-memory-render" {
		if err := experiment.RenderExisting(os.Args[2]); err != nil {
			log.Fatal(err)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "day11-memory-report" {
		client, err := llm.NewOpenRouterClient(cfg.LLMAPIKey, cfg.LLMBaseURL, cfg.LLMModel)
		if err != nil {
			log.Fatal(err)
		}
		path, err := experiment.RunDay11(context.Background(), client, "reports/day11-memory")
		fmt.Println(path)
		if err != nil {
			log.Fatal(err)
		}
		return
	}
	if len(os.Args) == 2 && os.Args[1] == "day12-personalization-report" {
		client, err := llm.NewOpenRouterClient(cfg.LLMAPIKey, cfg.LLMBaseURL, cfg.LLMModel)
		if err != nil {
			log.Fatal("LLM configuration invalid")
		}
		path, err := experiment.RunDay12(context.Background(), client)
		fmt.Println(path)
		if err != nil {
			log.Fatal("Day12 report failed; inspect saved results")
		}
		return
	}
	if len(os.Args) == 2 && os.Args[1] == "day13-task-state-report" {
		path, err := experiment.RunDay13()
		fmt.Println(path)
		if err != nil {
			log.Fatal(err)
		}
		return
	}
	if len(os.Args) == 2 && os.Args[1] == "day14-invariants-report" {
		path, err := experiment.RunDay14()
		fmt.Println(path)
		if err != nil {
			log.Fatal(err)
		}
		return
	}
	if len(os.Args) == 2 && os.Args[1] == "day15-transitions-report" {
		path, e := experiment.RunDay15()
		fmt.Println(path)
		if e != nil {
			log.Fatal(e)
		}
		return
	}
	if len(os.Args) == 2 && os.Args[1] == "semantic-report" {
		client, err := llm.NewOpenRouterClient(cfg.LLMAPIKey, cfg.LLMBaseURL, cfg.LLMModel)
		if err != nil {
			log.Fatal("LLM configuration invalid")
		}
		path, err := experiment.RunSemantic(context.Background(), client)
		fmt.Println(path)
		if err != nil {
			log.Fatal("semantic report failed")
		}
		return
	}

	if len(os.Args) > 1 {
		log.Fatal("Неизвестная команда. Используйте setup, memory, day11-memory-report или диагностические команды Identity & Access.")
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
	wsSvc.InviteTTL = cfg.InviteTTL
	wsSvc.InviteMaxUses = cfg.InviteMaxUses
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
	wbAPI := wildberries.NewWithProfile(os.Getenv("WB_API_TOKEN"), os.Getenv("WB_API_PROFILE"))
	marketplaceSvc, err := marketplace.New(wsSvc.DB(), wbAPI)
	if err != nil {
		log.Fatal("marketplace initialization failed")
	}
	defer marketplaceSvc.Close()
	agentSvc.Marketplace = marketplaceSvc
	jobs := background.New(wsSvc.DB(), nil, nil)
	manager, err := mcpmanager.New(context.Background(), wbAPI, jobs)
	if err != nil {
		log.Fatal("MCP initialization failed")
	}
	defer manager.Close()
	agentSvc.MCP = manager.Client
	jobs.WB.MCP = manager.Client
	agentSvc.Memory.MaxMessages = cfg.ShortTermMaxMessages
	agentSvc.Memory.MaxTokens = cfg.ShortTermMaxTokens
	_ = agentSvc

	bot, err := telegram.NewBot(cfg.TelegramBotToken, wsSvc, invSvc, prodSvc, auditSvc, agentSvc)
	if err != nil {
		log.Fatalf("failed to initialize Telegram bot: %v", err)
	}

	_ = context.Background()
	bot.Marketplace = marketplaceSvc
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	jobs.Sender = bot
	bot.Background = jobs
	bot.DailySummary = manager
	bot.JobContext = ctx
	jobs.Scheduler.Start(ctx)
	defer jobs.Scheduler.Close()
	fmt.Println("WorkshopAgent готов. Telegram token и LLM настроены из .env.")
	bot.StartContext(ctx)
	jobs.Scheduler.Close()
	bot.WaitBackground()
}
