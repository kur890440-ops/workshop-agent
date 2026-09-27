package experiment

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"time"

	"github.com/joho/godotenv"
	"workshop-agent/internal/integrations/mcpclient"
	"workshop-agent/internal/integrations/mcpmanager"
	"workshop-agent/internal/marketplace"
	wb "workshop-agent/internal/marketplace/wildberries"
)

type wbAuditReport struct {
	At             time.Time         `json:"at"`
	Probe          bool              `json:"live_prices_probe"`
	Workshop       int64             `json:"workshop_id"`
	Enabled        bool              `json:"enabled"`
	ProfilePresent bool              `json:"saved_profile_present"`
	ProfileFetched string            `json:"saved_profile_fetched"`
	Before         wb.Diagnostics    `json:"before"`
	After          wb.Diagnostics    `json:"after"`
	History        []wb.RequestTrace `json:"history"`
	ProbeResult    string            `json:"probe_result"`
	Audit          string            `json:"audit"`
}

// RunWBRateAudit does not start Telegram, jobs, migrations, or an extra process.
// Default is read-only. Explicit live mode persists only technical cooldowns in
// the existing table, so a fresh 429 cannot be lost before the next application start.
// Run live mode only while the normal application is stopped (one WB client).
func RunWBRateAudit(ctx context.Context, probe bool) (string, error) {
	values, e := godotenv.Read(".env")
	if e != nil && !os.IsNotExist(e) {
		return "", errors.New("configuration unavailable")
	}
	get := func(key string) string {
		if v := os.Getenv(key); v != "" {
			return v
		}
		return values[key]
	}
	dbpath := get("DATABASE_PATH")
	if dbpath == "" {
		dbpath = "data/workshop.db"
	}
	abs, e := filepath.Abs(dbpath)
	if e != nil {
		return "", errors.New("database path unavailable")
	}
	mode := "ro"
	if probe {
		mode = "rw"
	}
	db, e := sql.Open("sqlite", "file:"+filepath.ToSlash(abs)+"?mode="+mode)
	if e != nil {
		return "", errors.New("database unavailable")
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, e = db.Exec(`PRAGMA busy_timeout=5000`); e != nil {
		return "", errors.New("database unavailable")
	}
	r := wbAuditReport{At: time.Now().UTC(), Probe: probe, ProbeResult: "NOT_REQUESTED"}
	var profileID, stamp string
	e = db.QueryRow(`SELECT workshop_id,enabled,seller_id,checked_at FROM marketplace_connections WHERE id=1`).Scan(&r.Workshop, &r.Enabled, &profileID, &stamp)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return "", errors.New("connection unavailable")
	}
	r.ProfilePresent = profileID != ""
	if parsed, err := time.Parse(time.RFC3339Nano, stamp); err == nil {
		r.ProfileFetched = parsed.UTC().Format(time.RFC3339Nano)
	}
	api := wb.New(get("WB_API_TOKEN"))
	// Existing deadlines remain authoritative; no identity refresh is allowed here.
	store := marketplace.MCPCooldowns(db)
	api.SetCooldownStore(store)
	var identityColumn int
	if db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('marketplace_connections') WHERE name='identity_fingerprint'`).Scan(&identityColumn) == nil && identityColumn > 0 {
		if identities, ok := store.(wb.IdentityStore); ok {
			api.SetIdentityStore(identities)
		}
	}
	r.Before, e = api.Diagnostics()
	if e != nil {
		return "", errors.New("saved cooldown unavailable")
	}
	fmt.Println("Token type:", r.Before.TokenType, "(local decode; signature not verified)")
	for _, v := range r.Before.Rates {
		fmt.Printf("%s: blocked=%t retry_not_before=%s source=%s\n", v.Group, v.Blocked, v.RetryAt.UTC().Format(time.RFC3339), v.Source)
	}
	fmt.Println("Seller-info HTTP: disabled in this diagnostic")
	if probe {
		if !r.Enabled || !r.ProfilePresent {
			return "", errors.New("active attached cabinet required")
		}
		api.LimitToOneRequest()
		manager, err := mcpmanager.New(ctx, api, nil)
		if err != nil {
			return "", errors.New("MCP unavailable")
		}
		limited, cancel := context.WithTimeout(ctx, 35*time.Second)
		err = manager.Client.DiagnosticPrices(limited, r.Workshop)
		cancel()
		closeErr := manager.Close()
		r.ProbeResult = "COMPLETE"
		if err != nil {
			r.ProbeResult = "CONTROLLED_FAILURE"
			var rate *wb.RateLimitError
			var code mcpclient.Error
			if errors.As(err, &rate) {
				r.ProbeResult = "WB_RATE_LIMITED"
			} else if errors.As(err, &code) {
				switch code {
				case "wb_result_limit":
					r.ProbeResult = "BOUNDED_PROBE_ONLY_NOT_A_FULL_IMPORT"
				case "wb_authentication_error", "wb_access_denied", "wb_timeout", "wb_api_error", "wb_configuration_error":
					r.ProbeResult = string(code)
				}
			}
		}
		if closeErr != nil {
			return "", errors.New("MCP cleanup failed")
		}
	}
	r.History = api.History()
	r.After, e = api.Diagnostics()
	if e != nil {
		return "", errors.New("cooldown unavailable")
	}
	rawAudit, e := os.ReadFile("docs/wb-rate-limit-audit.md")
	if e != nil {
		return "", errors.New("audit document unavailable")
	}
	r.Audit = string(rawAudit)
	dir := filepath.Join("reports", "wb-rate-limit-audit", r.At.Format("20060102T150405.000000000Z"))
	if e = os.MkdirAll(dir, 0700); e != nil {
		return "", errors.New("report unavailable")
	}
	raw, e := json.MarshalIndent(r, "", "  ")
	if e != nil {
		return "", errors.New("report unavailable")
	}
	if api.ContainsSecret(string(raw)) {
		return "", errors.New("report safety check failed")
	}
	if e = os.WriteFile(filepath.Join(dir, "audit.json"), raw, 0600); e != nil {
		return "", errors.New("report unavailable")
	}
	view := struct {
		At    string
		Data  string
		Audit string
	}{r.At.Format(time.RFC3339), string(raw), r.Audit}
	tmpl := template.Must(template.New("audit").Parse(`<!doctype html><html lang="ru"><meta charset="utf-8"><title>WB rate-limit audit</title><style>body{font:16px system-ui;max-width:1100px;margin:40px auto;padding:0 20px;color:#162536}pre{white-space:pre-wrap;overflow-wrap:anywhere;background:#f2f5f8;padding:20px;border-radius:8px}h1,h2{color:#16446b}</style><h1>WB rate-limit audit</h1><p>{{.At}}</p><p>Один executable / процесс / MCP in-memory. Секреты и тела ответов исключены. До фиксации trace точное историческое число HTTP неизвестно.</p><h2>Callers, endpoints, before / after, изменения и документация</h2><pre>{{.Audit}}</pre><h2>Фактическая диагностика: тип токена, сохранённые сроки, headers, 429, local blocks</h2><pre>{{.Data}}</pre></html>`))
	path := filepath.Join(dir, "report.html")
	f, e := os.Create(path)
	if e != nil {
		return "", errors.New("report unavailable")
	}
	e = tmpl.Execute(f, view)
	closeErr := f.Close()
	if e != nil || closeErr != nil {
		return "", errors.New("report unavailable")
	}
	fmt.Println("Probe:", r.ProbeResult)
	for _, trace := range r.History {
		fmt.Printf("%s %s tool=%s %s HTTP=%d result=%s\n", trace.Timestamp.Format(time.RFC3339), trace.Caller, trace.Tool, trace.Endpoint, trace.HTTPStatus, trace.Result)
	}
	return path, nil
}
