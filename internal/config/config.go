package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"workshop-agent/internal/speech"
	"workshop-agent/internal/webchat"

	"github.com/joho/godotenv"
)

type Config struct {
	Hybrid                      HybridConfig
	Web                         webchat.Config
	Speech                      speech.Config
	SpeechPaths                 SpeechPaths
	TelegramBotToken            string
	TelegramVoiceShowTranscript bool
	LLMAPIKey                   string
	LLMBaseURL                  string
	LLMModel                    string
	DatabasePath                string
	InviteTTL                   time.Duration
	InviteMaxUses               int
	ShortTermMaxMessages        int
	ShortTermMaxTokens          int
}

func Load() (*Config, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	_ = godotenv.Load(filepath.Join(applicationRoot(cwd, executable), ".env"))
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
	cfg.Hybrid, err = loadHybrid()
	if err != nil {
		return nil, err
	}
	cfg.Web = webchat.Config{ListenAddress: "127.0.0.1:8080", PublicBaseURL: "http://127.0.0.1:8080", SessionTTL: 12 * time.Hour, LoginCodeTTL: 2 * time.Minute}
	if value := os.Getenv("WEB_ENABLED"); value != "" {
		cfg.Web.Enabled, err = strconv.ParseBool(value)
		if err != nil {
			return nil, fmt.Errorf("invalid WEB_ENABLED")
		}
	}
	if value := os.Getenv("WEB_LISTEN_ADDRESS"); value != "" {
		cfg.Web.ListenAddress = value
	}
	if value := os.Getenv("WEB_PUBLIC_BASE_URL"); value != "" {
		cfg.Web.PublicBaseURL = value
	}
	for key, target := range map[string]*time.Duration{"WEB_SESSION_TTL": &cfg.Web.SessionTTL, "WEB_LOGIN_CODE_TTL": &cfg.Web.LoginCodeTTL} {
		if value := os.Getenv(key); value != "" {
			*target, err = time.ParseDuration(value)
			if err != nil {
				return nil, fmt.Errorf("invalid %s", key)
			}
		}
	}
	if value := os.Getenv("WEB_TRUSTED_PROXIES"); value != "" {
		for _, v := range strings.Split(value, ",") {
			cfg.Web.TrustedProxies = append(cfg.Web.TrustedProxies, strings.TrimSpace(v))
		}
	}
	if err = cfg.Web.Validate(); err != nil {
		return nil, err
	}
	var speechErr error
	cfg.Speech, speechErr = loadSpeech()
	if speechErr != nil {
		return nil, speechErr
	}
	cfg.TelegramVoiceShowTranscript, err = loadVoiceShowTranscript()
	if err != nil {
		return nil, err
	}
	cfg.SpeechPaths = resolveSpeechPaths(&cfg.Speech, cwd, executable)
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
