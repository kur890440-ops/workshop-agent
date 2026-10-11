package config

import (
	"testing"
	"workshop-agent/internal/llm"
)

func TestHybridConfig(t *testing.T) {
	for _, key := range []string{"LLM_MODE", "LLM_LOCAL_MODEL", "LLM_LOCAL_BASE_URL", "LLM_LOCAL_TIMEOUT", "LLM_CLOUD_PROVIDER", "LLM_CLOUD_TIMEOUT"} {
		t.Setenv(key, "")
	}
	cfg, err := loadHybrid()
	if err != nil || cfg.Mode != llm.Auto || cfg.LocalModel != "" {
		t.Fatal(cfg, err)
	}
	for _, mode := range []llm.Mode{llm.LocalOnly, llm.Auto, llm.CloudOnly} {
		t.Setenv("LLM_MODE", string(mode))
		if _, err := loadHybrid(); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("LLM_MODE", "AUTO")
	for key, value := range map[string]string{"LLM_MODE": "unknown", "LLM_LOCAL_BASE_URL": "https://remote.example", "LLM_LOCAL_TIMEOUT": "0s", "LLM_CLOUD_TIMEOUT": "10h", "LLM_LOCAL_MODEL": "qwen-cloud", "LLM_CLOUD_PROVIDER": "unknown"} {
		t.Run(key, func(t *testing.T) {
			t.Setenv(key, value)
			if _, err := loadHybrid(); err == nil {
				t.Fatal("accepted invalid config")
			}
		})
	}
}
