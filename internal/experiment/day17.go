package experiment

import (
	"context"
	"encoding/json"
	"errors"
	"html/template"
	"os"
	"path/filepath"
	"strings"
	"time"

	"workshop-agent/internal/agent"
	"workshop-agent/internal/integrations/mcpclient"
	"workshop-agent/internal/integrations/wbmcp"
	"workshop-agent/internal/integrations/wbmcpfixture"
	"workshop-agent/internal/marketplace"
	"workshop-agent/internal/workshops"
)

// RunDay17 uses the actual application handler, SDK client and server over OS
// pipes. Only the downstream WB API is a mock. No config.Load, bot or live DB.
func RunDay17(ctx context.Context, executable string, args []string, root string) (string, error) {
	dir := filepath.Join(root, time.Now().UTC().Format("20060102T150405.000000000Z"))
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	ws := workshops.NewService(filepath.Join(dir, "fixture.db"))
	defer ws.Close()
	user, err := ws.UpsertUser(991701, "", "Day17 fixture", "")
	if err != nil {
		return "", err
	}
	workshop, err := ws.CreateOwnedWorkshop(user, "Day17 isolated workshop")
	if err != nil {
		return "", err
	}
	mp, err := marketplace.New(ws.DB(), wbmcpfixture.API{})
	if err != nil {
		return "", err
	}
	defer mp.Close()
	scope := marketplace.Scope{UserID: user, WorkshopID: workshop}
	id, err := mp.Attach(scope)
	if err != nil {
		return "", err
	}
	// Explicit fixture identity, not a successful real account check.
	if _, err = ws.DB().Exec(`UPDATE marketplace_connections SET seller_id='day17-fixture' WHERE id=?`, id); err != nil {
		return "", err
	}
	client := mcpclient.NewCommand(executable, args...)
	defer client.Close()
	a := &agent.WorkshopAgent{WS: ws, Marketplace: mp, MCP: client}
	answer, result, callErr := a.MCPStocks(ctx, user, workshop, true)
	closeErr := client.Close()
	state := client.State()
	passed := callErr == nil && closeErr == nil && len(result.Value.Data) == 3 && strings.Contains(answer, "Получено через MCP") && state.SessionClosed && state.ChildExited && state.WriteToolsExposed == 0
	var spec wbmcp.ToolSpec
	for _, tool := range wbmcp.Tools() {
		if tool.Name == mcpclient.StocksTool {
			spec = tool
		}
	}
	schema := ""
	for _, tool := range state.Tools {
		if tool.Name == mcpclient.StocksTool {
			schema = string(tool.InputSchema)
		}
	}
	r := struct {
		Mode, Command, Response, Schema string
		Passed                          bool
		Tool                            wbmcp.ToolSpec
		Result                          mcpclient.StocksResult
		State                           mcpclient.Discovery
	}{"MOCK WB / real MCP STDIO / actual WorkshopAgent.MCPStocks", "/wb_stocks trace", answer, schema, passed, spec, result, state}
	raw, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return "", err
	}
	if err = os.WriteFile(filepath.Join(dir, "result.json"), raw, 0600); err != nil {
		return "", err
	}
	t := template.Must(template.New("report").Parse(`<!doctype html><html lang="ru"><meta charset="utf-8"><title>Day 17 — MCP tool</title><style>body{font:16px system-ui;max-width:1000px;margin:32px auto;padding:20px}pre{white-space:pre-wrap;background:#eef3f6;padding:16px}dt{font-weight:bold}</style><h1>Day 17 — первый инструмент MCP</h1><p>Проверка: {{.Passed}}. {{.Mode}}</p><p>Telegram delivery не запускалась; вход в прикладной обработчик из CLI отчёта. Реальный WB API не вызывался.</p><pre>Telegram /wb_stocks (production entry)
↓ Workshop Agent.MCPStocks (executed in this report)
↓ Marketplace authorization
↓ MCP Client → tools/call → STDIO
↓ existing WB MCP Server → wb_get_wb_stocks
↓ WBStocks (mock API here; Wildberries API in production)
↑ structured result → human response</pre><dl><dt>Tool</dt><dd>{{.Tool.Name}}</dd><dt>Description</dt><dd>{{.Tool.Description}}</dd><dt>Go method</dt><dd>{{.Tool.GoMethod}}</dd><dt>Classification</dt><dd>READ_ONLY={{.Tool.ReadOnly}} WRITE={{.Tool.Write}} DESTRUCTIVE={{.Tool.Destructive}} REQUIRES_CONFIRMATION={{.Tool.RequiresConfirmation}}</dd><dt>Input schema</dt><dd><pre>{{.Schema}}</pre></dd><dt>Actual input</dt><dd>{}</dd></dl><h2>Результат приложения</h2><pre>{{.Response}}</pre><h2>Проверки транспорта</h2><p>Tools: {{len .State.Tools}}, write tools: {{.State.WriteToolsExposed}}; protocol: {{.State.Protocol}}; session closed: {{.State.SessionClosed}}; child exited: {{.State.ChildExited}}</p><h2>Structured output</h2><p>source, fetched_at, complete, untrusted_data, data[nmId, chrtId, warehouseId, warehouseName, quantity]. Полный фактический результат — <a href="result.json">result.json</a>.</p></html>`))
	path := filepath.Join(dir, "report.html")
	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	err = t.Execute(f, r)
	closeFileErr := f.Close()
	if err != nil {
		return path, err
	}
	if closeFileErr != nil {
		return path, closeFileErr
	}
	if !passed {
		return path, errors.New("day17 mock flow failed; inspect report")
	}
	return path, nil
}
