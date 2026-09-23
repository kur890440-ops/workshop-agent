package mcpclient

import (
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"os"
	"path/filepath"
)

func Print(w io.Writer, d Discovery) error {
	if _, err := fmt.Fprintf(w, "MCP connection: OK\nServer: %s\nTransport: %s\nProtocol: %s\nTools discovered: %d\n", d.Server, d.Transport, d.Protocol, len(d.Tools)); err != nil {
		return err
	}
	for i, t := range d.Tools {
		if _, err := fmt.Fprintf(w, "%d. %s\n   %s\n", i+1, t.Name, t.Description); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(w, "Write tools exposed: %d\nMCP tool discovery: OK\nSession closed: %t\nChild process exited: %t\n", d.WriteToolsExposed, d.SessionClosed, d.ChildExited)
	return err
}
func WriteReport(dir string, d Discovery) (string, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	raw, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return "", err
	}
	if err = os.WriteFile(filepath.Join(dir, "discovery.json"), raw, 0600); err != nil {
		return "", err
	}
	f, err := os.Create(filepath.Join(dir, "report.html"))
	if err != nil {
		return "", err
	}
	err = reportTemplate.Execute(f, d)
	closeErr := f.Close()
	if err != nil {
		return "", err
	}
	if closeErr != nil {
		return "", closeErr
	}
	return filepath.Join(dir, "report.html"), nil
}

var reportTemplate = template.Must(template.New("report").Parse(`<!doctype html>
<html lang="ru"><meta charset="utf-8"><meta name="viewport" content="width=device-width"><title>Day 16 — MCP</title>
<style>body{font:16px system-ui;margin:36px auto;max-width:1000px;padding:0 20px;color:#15243a;background:#f4f7fc}h1{font-size:32px}.flow{display:flex;gap:8px;align-items:center;flex-wrap:wrap}.box,section{background:white;padding:18px;border-radius:12px;border:1px solid #d5dfec}section{margin:20px 0}table{border-collapse:collapse;width:100%}td,th{text-align:left;border-bottom:1px solid #d5dfec;padding:12px}code{overflow-wrap:anywhere}.ok{color:#136643}</style>
<h1>Day 16 · WB MCP</h1><p class="ok">Connection: OK · Tools discovered: {{len .Tools}} · Write tools exposed: {{.WriteToolsExposed}}</p>
<div class="flow"><div class="box">Workshop Agent MCP Client</div>→<div class="box">STDIO</div>→<div class="box">WB MCP Server</div>→<div class="box">Existing Go WB API Client</div>→<div class="box">Wildberries API<br><small>не вызывался</small></div></div>
<section><h2>Фактический протокольный результат</h2><p>Server: {{.Server}} · Protocol: {{.Protocol}} · Transport: {{.Transport}}</p><p>Session closed: {{.SessionClosed}} · Child process exited: {{.ChildExited}}</p><table><tr><th>Tool из tools/list</th><th>Описание</th><th>READ ONLY</th></tr>{{range .Tools}}<tr><td><code>{{.Name}}</code></td><td>{{.Description}}</td><td>{{.ReadOnly}}</td></tr>{{end}}</table></section>
<section><h2>Границы проверки</h2><p>Реальный дочерний процесс, MCP initialize и tools/list через STDIO. Server запущен с -no-token. TCP listener, реальные WB/Telegram запросы и LLM tool calling не использовались. Аннотации не заменяют будущую проверку прав.</p><p>Полный ответ discovery со схемами: <a href="discovery.json">discovery.json</a>.</p></section></html>`))
