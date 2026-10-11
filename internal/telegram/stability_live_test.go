package telegram

import (
	"context"
	"os"
	"testing"
	"time"
	"workshop-agent/internal/llm"
)

func TestStabilityLiveApplicationReadOnly(t *testing.T) {
	if os.Getenv("STABILITY_LIVE_LOCAL") != "1" {
		t.Skip("explicit local read-only application smoke")
	}
	h, _, cloud, _ := hybridFixture(t)
	local, e := llm.NewOllamaClient("http://127.0.0.1:11434", "qwen3:4b-q4_K_M", 60*time.Second)
	if e != nil {
		t.Fatal(e)
	}
	local.RequireCapabilities = true
	if _, _, e = local.Capabilities(context.Background()); e != nil {
		t.Fatal(e)
	}
	h.bot.Hybrid.Local = local
	h.bot.Hybrid.Policy.Mode = llm.LocalOnly
	rt := installRuntime(t, h)
	web := connectWeb(t, h)
	start := time.Now()
	for _, question := range []string{
		"Объясни разницу между PETG и ABS для 3D-печати.",
		"Почему повышение температуры сопла может ухудшить качество печати?",
		"почему земной шар круглый?",
	} {
		web.post(t, "/api/chat/messages", question)
		var status, code, route string
		if e = web.service.DB.QueryRow("SELECT status,error_code FROM web_requests ORDER BY rowid DESC LIMIT 1").Scan(&status, &code); e != nil || status != "COMPLETED" {
			t.Fatal(question, status, code, e)
		}
		if e = web.service.DB.QueryRow("SELECT json_extract(metadata,'$.route') FROM hybrid_llm_runs ORDER BY rowid DESC LIMIT 1").Scan(&route); e != nil || route != "GENERAL" {
			t.Fatal(question, route, e)
		}
		t.Logf("REAL local shared application: %q -> COMPLETED / %s", question, route)
	}
	var status, code string
	if e = web.service.DB.QueryRow("SELECT status,error_code FROM web_requests ORDER BY rowid DESC LIMIT 1").Scan(&status, &code); e != nil || status != "COMPLETED" {
		t.Fatal(status, code, e)
	}
	history := web.history(t)
	if len(history) < 2 {
		t.Fatal("missing output")
	}
	if cloud.calls != 0 {
		t.Fatal("cloud called")
	}
	h.message(t, 900001, "/limits set rate 21")
	if rt.Settings.Get().Rate != 21 {
		t.Fatal("admin settings")
	}
	h.message(t, 900001, "/limits set rate 20")
	t.Logf("REAL Ollama + shared Bot + async Web on isolated DB:202->COMPLETED elapsed_ms=%d; Telegram /limits command handler REAL, Telegram API MOCK; cloud_calls=0", time.Since(start).Milliseconds())
}
