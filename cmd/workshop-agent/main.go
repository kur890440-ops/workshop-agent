package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"time"
	"workshop-agent/internal/webchat"

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
	"workshop-agent/internal/marketplace/ozon"
	"workshop-agent/internal/marketplace/wildberries"
	"workshop-agent/internal/marketplacequery"
	"workshop-agent/internal/products"
	"workshop-agent/internal/speech"
	"workshop-agent/internal/stability"
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
	if len(os.Args) >= 2 && os.Args[1] == "wb-rate-audit" {
		probe := len(os.Args) == 3 && os.Args[2] == "--probe-prices"
		if len(os.Args) > 3 || len(os.Args) == 3 && !probe {
			log.Fatal("Usage: wb-rate-audit [--probe-prices]")
		}
		path, e := experiment.RunWBRateAudit(context.Background(), probe)
		if path != "" {
			fmt.Println("Report:", path)
		}
		if e != nil {
			log.Fatal("WB diagnostic failed; inspect safe report")
		}
		return
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
	if len(os.Args) == 2 && os.Args[1] == "wb-stock-sources-report" {
		path, e := experiment.RunStockSources(context.Background())
		fmt.Println(path)
		if e != nil {
			log.Fatal("Stock sources mock report failed: ", e)
		}
		return
	}
	if len(os.Args) == 2 && os.Args[1] == "day19-mcp-report" {
		path, e := experiment.RunDay19(context.Background())
		fmt.Println(path)
		if e != nil {
			log.Fatal("Day19 mock report failed: ", e)
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
	if len(os.Args) > 1 && os.Args[1] == "speech" {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		if e := speech.RunCLI(ctx, cfg.Speech, os.Args[2:], os.Stdout); e != nil {
			log.Print(e)
			os.Exit(1)
		}
		return
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
		if cfg.Hybrid.Mode == llm.LocalOnly {
			log.Fatal("LOCAL_ONLY: cloud experiment disabled")
		}
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
		if cfg.Hybrid.Mode == llm.LocalOnly {
			log.Fatal("LOCAL_ONLY: cloud experiment disabled")
		}
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
		if cfg.Hybrid.Mode == llm.LocalOnly {
			log.Fatal("LOCAL_ONLY: cloud experiment disabled")
		}
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
	if cfg.Hybrid.Mode == llm.CloudOnly && cfg.LLMAPIKey == "" {
		log.Fatal("LLM_API_KEY is required in .env")
	}
	if cfg.Hybrid.Mode == llm.CloudOnly && cfg.LLMBaseURL == "" {
		log.Fatal("LLM_BASE_URL is required in .env")
	}
	if cfg.Hybrid.Mode == llm.CloudOnly && cfg.LLMModel == "" {
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

	llmClient, err := newHybridClient(cfg)
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
	ozonSvc := ozon.NewService(wsSvc.DB(), os.Getenv("OZON_CLIENT_ID"), os.Getenv("OZON_API_KEY"))
	defer ozonSvc.Close()
	manager, err := mcpmanager.New(context.Background(), wbAPI, jobs, ozonSvc)
	if err != nil {
		log.Fatal("MCP initialization failed")
	}
	defer manager.Close()
	agentSvc.MCP = manager.Client
	jobs.WB.MCP = manager.Client
	marketplaceSvc.SetStockReader(manager.Client)
	agentSvc.Memory.MaxMessages = cfg.ShortTermMaxMessages
	agentSvc.Memory.MaxTokens = cfg.ShortTermMaxTokens
	_ = agentSvc

	bot, err := telegram.NewBot(cfg.TelegramBotToken, wsSvc, invSvc, prodSvc, auditSvc, agentSvc)
	if err != nil {
		log.Fatalf("failed to initialize Telegram bot: %v", err)
	}

	_ = context.Background()
	bot.Marketplace = marketplaceSvc
	bot.Hybrid = llmClient
	baseLimits, e := stability.FromEnv()
	if e != nil {
		log.Fatal(e)
	}
	limitStore, e := stability.NewStore(wsSvc.DB(), baseLimits)
	if e != nil {
		log.Fatal(e)
	}
	localCapacity := 0
	if local, ok := llmClient.Local.(*llm.OllamaClient); ok {
		local.RequireCapabilities = true
		cctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		localCapacity, _, _ = local.Capabilities(cctx)
		cancel()
	}
	cloudCapacity := 0
	if cloud, ok := llmClient.Cloud.(*llm.OpenRouterClient); ok {
		cloud.RequireCapabilities = true
		cc, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		cloudCapacity, _ = cloud.Capabilities(cc)
		cancel()
	}
	localHardwareCapacity := 1
	if value := os.Getenv("LIMIT_LOCAL_CAPACITY"); value != "" {
		n, e := strconv.Atoi(value)
		if e != nil || n < 1 || n > 4 {
			log.Fatal("invalid LIMIT_LOCAL_CAPACITY")
		}
		localHardwareCapacity = n
	}
	if limitStore.Get().Local > localHardwareCapacity {
		log.Fatal("local concurrency exceeds operator-validated hardware capacity")
	}
	limitStore.ValidateModel = func(s stability.Settings) error {
		if s.Local > localHardwareCapacity {
			return fmt.Errorf("HARDWARE_CAPACITY_UNVERIFIED: exceeds LIMIT_LOCAL_CAPACITY")
		}
		if localCapacity <= 0 {
			return fmt.Errorf("MODEL_CAPACITY_UNVERIFIED")
		}
		if llmClient.Cloud != nil && (cloudCapacity <= 0 || s.MaxContext > cloudCapacity) {
			return fmt.Errorf("CLOUD_MODEL_CAPACITY_UNVERIFIED_OR_UNSUPPORTED")
		}
		if s.MaxContext > localCapacity {
			return fmt.Errorf("MODEL_CONTEXT_UNSUPPORTED")
		}
		return nil
	}
	if localCapacity > 0 && limitStore.Get().MaxContext > localCapacity {
		log.Fatal("runtime context exceeds model capacity")
	}
	runtimeLimits := stability.New(limitStore)
	bot.EnableLimits(runtimeLimits)

	bot.Ozon = ozonSvc
	bot.OzonTools = manager
	bot.MarketQuery = &marketplacequery.MarketplaceOrchestrator{
		Store: &marketplacequery.Repository{DB: wsSvc.DB(), Sensitive: func(s string) bool { return marketplaceSvc.SensitiveInput(s) || ozonSvc.SensitiveInput(s) }},
		MCP:   &mcpmanager.QueryAdapter{Manager: manager, WB: marketplaceSvc, Ozon: ozonSvc},
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	speechProvider := speech.New(cfg.Speech)
	defer speechProvider.Close()
	bot.ConfigureSpeech(speechProvider, cfg.Speech)
	bot.VoiceShowTranscript = cfg.TelegramVoiceShowTranscript
	logSpeechStartup(cfg, speechProvider.Status(ctx))
	jobs.Sender = bot
	bot.Background = jobs
	bot.DailySummary = manager
	bot.JobContext = ctx
	stopWeb := func() {}
	if cfg.Web.Enabled {
		web, err := webchat.New(wsSvc.DB(), cfg.Web)
		if err != nil {
			log.Fatal(err)
		}
		web.Runtime = runtimeLimits
		web.Root = ctx
		_, err = wsSvc.DB().Exec("UPDATE web_requests SET status='CANCELLED',error_code='PROCESS_RESTARTED',updated_at=unixepoch() WHERE status IN ('QUEUED','RUNNING')")
		if err != nil {
			log.Fatal("request recovery failed")
		}
		web.Handle = bot.HandleInbound
		bot.WebLogin = web.ConfirmLogin
		bot.WebOutput = web.Deliver
		jobs.Scheduler.Maintenance = web.Cleanup
		listener, err := net.Listen("tcp", cfg.Web.ListenAddress)
		if err != nil {
			log.Fatal("Web Chat listener failed: ", err)
		}
		server := &http.Server{BaseContext: func(net.Listener) context.Context { return ctx }, Handler: web.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 20 * time.Second, WriteTimeout: 5 * time.Minute, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16384}
		webDone := make(chan struct{})
		go func() {
			defer close(webDone)
			if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
				log.Print("Web Chat HTTP server stopped")
				stop()
			}
		}()
		var closeOnce sync.Once
		stopWeb = func() {
			closeOnce.Do(func() {
				shutdown, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				if server.Shutdown(shutdown) != nil {
					_ = server.Close()
				}
				<-webDone
			})
		}
		defer stopWeb()
		log.Printf("Web Chat: %s/chat", cfg.Web.PublicBaseURL)
	}
	jobs.Scheduler.Start(ctx)
	defer jobs.Scheduler.Close()
	fmt.Println("WorkshopAgent готов. Telegram token и LLM настроены из .env.")
	bot.StartContext(ctx)
	stop()
	runtimeLimits.Wait()
	stopWeb()
	jobs.Scheduler.Close()
	bot.WaitBackground()
}
