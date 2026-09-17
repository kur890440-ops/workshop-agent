package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	TelegramBotToken     string
	LLMAPIKey            string
	LLMBaseURL           string
	LLMModel             string
	DatabasePath         string
	InviteTTL            time.Duration
	InviteMaxUses        int
	ShortTermMaxMessages int
	ShortTermMaxTokens   int
}

func Load() (*Config, error) {
	_ = godotenv.Load()
	cfg := &Config{
		TelegramBotToken: os.Getenv("TELEGRAM_BOT_TOKEN"),
		LLMAPIKey:        os.Getenv("LLM_API_KEY"),
		LLMBaseURL:       os.Getenv("LLM_BASE_URL"),
		LLMModel:         os.Getenv("LLM_MODEL"),
		DatabasePath:     os.Getenv("DATABASE_PATH"),
	}
	if cfg.DatabasePath == "" {
		cfg.DatabasePath = "./data/workshop.db"
	}
	cfg.InviteTTL = 24 * time.Hour
	cfg.InviteMaxUses = 1
	cfg.ShortTermMaxMessages = 20
	cfg.ShortTermMaxTokens = 1600
	for key, target := range map[string]*int{"SHORT_TERM_MAX_MESSAGES": &cfg.ShortTermMaxMessages, "SHORT_TERM_MAX_TOKENS": &cfg.ShortTermMaxTokens} {
		if value := os.Getenv(key); value != "" {
			n, err := strconv.Atoi(value)
			if err != nil || n < 1 || n > 10000 {
				return nil, fmt.Errorf("invalid %s", key)
			}
			*target = n
		}
	}
	if value := os.Getenv("INVITE_TTL"); value != "" {
		ttl, err := time.ParseDuration(value)
		if err != nil || ttl <= 0 {
			return nil, fmt.Errorf("invalid INVITE_TTL")
		}
		cfg.InviteTTL = ttl
	}
	if value := os.Getenv("INVITE_MAX_USES"); value != "" {
		n, err := strconv.Atoi(value)
		if err != nil || n < 1 {
			return nil, fmt.Errorf("invalid INVITE_MAX_USES")
		}
		cfg.InviteMaxUses = n
	}
	if err := os.MkdirAll(filepath.Dir(cfg.DatabasePath), 0o755); err != nil {
		return nil, err
	}
	return cfg, nil
}
