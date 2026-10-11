package main

import (
	"context"
	"log"
	"time"
	"workshop-agent/internal/config"
	"workshop-agent/internal/llm"
)

func newHybridClient(cfg *config.Config) (*llm.HybridClient, error) {
	local, err := llm.NewOllamaClient(cfg.Hybrid.LocalBaseURL, cfg.Hybrid.LocalModel, cfg.Hybrid.LocalTimeout)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err = local.DiscoverModel(ctx); err != nil {
		log.Print("Hybrid: local model unavailable; no automatic download")
	} else {
		log.Printf("Hybrid: local model=%s mode=%s", local.Model, cfg.Hybrid.Mode)
	}
	client := &llm.HybridClient{Local: local, Policy: llm.LLMFallbackPolicy{Mode: cfg.Hybrid.Mode}}
	if cfg.Hybrid.Mode != llm.LocalOnly && cfg.LLMAPIKey != "" && cfg.LLMModel != "" {
		cloud, e := llm.NewOpenRouterClient(cfg.LLMAPIKey, cfg.LLMBaseURL, cfg.LLMModel)
		if e != nil {
			return nil, e
		}
		cloud.HTTPClient.Timeout = cfg.Hybrid.CloudTimeout
		client.Cloud = cloud
	}
	return client, nil
}
