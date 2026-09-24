package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"workshop-agent/internal/integrations/mcpclient"
	"workshop-agent/internal/integrations/wbmcpfixture"
	"workshop-agent/internal/marketplace"
	"workshop-agent/internal/workshops"
)

func TestDay17AppChild(t *testing.T) {
	if len(os.Args) < 3 || os.Args[len(os.Args)-2] != "--" {
		return
	}
	if wbmcpfixture.Run(context.Background(), os.Args[len(os.Args)-1]) != nil {
		os.Exit(2)
	}
	os.Exit(0)
}

// Only configuration status is available in-process. Any attempted direct WB
// method dispatch panics via the nil embedded interface, catching MCP bypasses.
type noDirectWB struct{ marketplace.API }

func (noDirectWB) Configured() bool           { return true }
func (noDirectWB) ContainsSecret(string) bool { return false }

func TestDay17ApplicationUsesMCPAndEnforcesScope(t *testing.T) {
	ws := workshops.NewService(filepath.Join(t.TempDir(), "fixture.db"))
	defer ws.Close()
	u, err := ws.UpsertUser(991701, "", "Owner", "")
	if err != nil {
		t.Fatal(err)
	}
	w, err := ws.CreateOwnedWorkshop(u, "WB workshop")
	if err != nil {
		t.Fatal(err)
	}
	svc, err := marketplace.New(ws.DB(), noDirectWB{})
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	id, err := svc.Attach(marketplace.Scope{UserID: u, WorkshopID: w})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ws.DB().Exec(`UPDATE marketplace_connections SET seller_id='day17-fixture' WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	exe, _ := os.Executable()
	client := mcpclient.NewCommand(exe, "-test.run=^TestDay17AppChild$", "--", "success")
	defer client.Close()
	a := &WorkshopAgent{WS: ws, Marketplace: svc, MCP: client}
	answer, usage, err := a.HandleMessageForWorkshop(context.Background(), w, u, 991701, "/wb_stocks trace")
	if err != nil || usage == nil || !strings.Contains(answer, "12 шт.") || !strings.Contains(answer, "Получено через MCP") || !strings.Contains(answer, "wildberries.Client.WBStocks") {
		t.Fatalf("%s %v", answer, err)
	}
	var count int
	if err = ws.DB().QueryRow(`SELECT COUNT(*) FROM marketplace_stocks`).Scan(&count); err != nil || count != 0 {
		t.Fatal("unexpected cache write", count, err)
	}
	if err = ws.DB().QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action='WB_MCP_STOCKS' AND actor_user_id=? AND workshop_id=?`, u, w).Scan(&count); err != nil || count != 1 {
		t.Fatal("audit missing", count, err)
	}
	other, err := ws.CreateOwnedWorkshop(u, "Other workshop")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = a.MCPStocks(context.Background(), u, w, false); err == nil {
		t.Fatal("stale workshop accepted")
	}
	if _, _, err = a.MCPStocks(context.Background(), u, other, false); err == nil {
		t.Fatal("unbound workshop accepted")
	}
	if _, _, err = a.MCPStocks(context.Background(), u+999, w, false); err == nil {
		t.Fatal("foreign user accepted")
	}
	if _, err = ws.DB().Exec(`UPDATE user_workshop_context SET active_workshop_id=? WHERE user_id=?`, w, u); err != nil {
		t.Fatal(err)
	}
	if err = svc.Disable(marketplace.Scope{UserID: u, WorkshopID: w, ConnectionID: id}); err != nil {
		t.Fatal(err)
	}
	if _, _, err = a.MCPStocks(context.Background(), u, w, false); err == nil {
		t.Fatal("disabled connection accepted")
	}
}

func TestDay17HumanErrorsAreClosed(t *testing.T) {
	for _, code := range []string{"wb_configuration_error", "wb_authentication_error", "wb_access_denied", "wb_rate_limit", "wb_timeout", "invalid_tool_arguments", "mcp_internal_error", "wb_api_error", "mcp_connection_error"} {
		message := MCPErrorMessage(mcpclient.Error(code))
		if message == "" || strings.Contains(message, code) {
			t.Fatal(code, message)
		}
	}
	if strings.Contains(MCPErrorMessage(mcpclient.Error("synthetic-secret-marker")), "synthetic-secret-marker") {
		t.Fatal("error leak")
	}
}
