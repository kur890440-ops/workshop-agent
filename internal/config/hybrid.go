package config

import (
	"fmt"
	"os"
	"strings"
	"time"
	"workshop-agent/internal/llm"
)

type HybridConfig struct {
	Mode                                    llm.Mode
	LocalModel, LocalBaseURL, CloudProvider string
	LocalTimeout, CloudTimeout              time.Duration
}

func loadHybrid() (HybridConfig, error) {
	c := HybridConfig{Mode: llm.Auto, LocalBaseURL: "http://127.0.0.1:11434", CloudProvider: "openrouter", LocalTimeout: 90 * time.Second, CloudTimeout: 60 * time.Second}
	if v := os.Getenv("LLM_MODE"); v != "" {
		c.Mode = llm.Mode(strings.ToUpper(v))
	}
	if c.Mode != llm.Auto && c.Mode != llm.LocalOnly && c.Mode != llm.CloudOnly {
		return c, fmt.Errorf("invalid LLM_MODE")
	}
	c.LocalModel = os.Getenv("LLM_LOCAL_MODEL")
	if v := os.Getenv("LLM_LOCAL_BASE_URL"); v != "" {
		c.LocalBaseURL = v
	}
	if v := os.Getenv("LLM_CLOUD_PROVIDER"); v != "" {
		c.CloudProvider = v
	}
	if c.CloudProvider != "openrouter" {
		return c, fmt.Errorf("unsupported LLM_CLOUD_PROVIDER")
	}
	for k, p := range map[string]*time.Duration{"LLM_LOCAL_TIMEOUT": &c.LocalTimeout, "LLM_CLOUD_TIMEOUT": &c.CloudTimeout} {
		if v := os.Getenv(k); v != "" {
			d, e := time.ParseDuration(v)
			if e != nil || d <= 0 || d > 5*time.Minute {
				return c, fmt.Errorf("invalid %s", k)
			}
			*p = d
		}
	}
	if _, e := llm.NewOllamaClient(c.LocalBaseURL, c.LocalModel, c.LocalTimeout); e != nil {
		return c, e
	}
	return c, nil
}
