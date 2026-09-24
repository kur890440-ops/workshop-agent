package telegram

import (
	"context"
	"os"
	"strings"
	"testing"

	"workshop-agent/internal/integrations/mcpclient"
	"workshop-agent/internal/integrations/wbmcpfixture"
	"workshop-agent/internal/marketplace"
)

func TestDay17TelegramChild(t *testing.T) {
	if len(os.Args) < 3 || os.Args[len(os.Args)-2] != "--" {
		return
	}
	if wbmcpfixture.Run(context.Background(), os.Args[len(os.Args)-1]) != nil {
		os.Exit(2)
	}
	os.Exit(0)
}
func TestDay17TelegramCommandResultAndErrors(t *testing.T) {
	for _, mode := range []string{"success", "missing", "rate", "api"} {
		t.Run(mode, func(t *testing.T) {
			h, u, w := wbFixture(t)
			id, err := h.bot.Marketplace.Attach(marketplace.Scope{UserID: u, WorkshopID: w})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = h.bot.WS.DB().Exec(`UPDATE marketplace_connections SET seller_id='day17-fixture' WHERE id=?`, id); err != nil {
				t.Fatal(err)
			}
			exe, _ := os.Executable()
			client := mcpclient.NewCommand(exe, "-test.run=^TestDay17TelegramChild$", "--", mode)
			defer client.Close()
			h.bot.Agent.MCP = client
			h.message(t, 900001, "/wb_stocks trace")
			h.bot.wbWait.Wait()
			answer := h.sent[len(h.sent)-1]["text"].(string)
			if strings.Contains(answer, "synthetic-secret-marker") {
				t.Fatal("secret leaked")
			}
			switch mode {
			case "success":
				if !strings.Contains(answer, "Получено через MCP") || !strings.Contains(answer, "12 шт.") || !strings.Contains(answer, "MCP TRACE") {
					t.Fatal(answer)
				}
			case "missing":
				if !strings.Contains(answer, "не настроен на MCP-сервере") {
					t.Fatal(answer)
				}
			case "rate":
				if !strings.Contains(answer, "ограничил частоту") {
					t.Fatal(answer)
				}
			case "api":
				if !strings.Contains(answer, "не вернул корректные данные") {
					t.Fatal(answer)
				}
			}
		})
	}
}
