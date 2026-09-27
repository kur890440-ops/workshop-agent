package marketplace

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"workshop-agent/internal/auth"
	wb "workshop-agent/internal/marketplace/wildberries"
)

// DiagnosticsText is owner/manage-only and local: it never invokes WB or LLM.
func (s *Service) DiagnosticsText(sc Scope) (string, error) {
	if err := authorize(s.db, sc, auth.MarketplaceManage); err != nil {
		return "", err
	}
	c, err := connection(s.db, sc, false)
	if err != nil {
		return "", err
	}
	api, ok := s.api.(interface {
		Diagnostics() (wb.Diagnostics, error)
	})
	if !ok {
		return "", ErrInput
	}
	d, err := api.Diagnostics()
	if err != nil {
		return "", ErrStorage
	}
	var b strings.Builder
	fmt.Fprintf(&b, "WB diagnostics · мастерская %d · enabled=%t\nToken type: %s (локальное декодирование, подпись не проверена)\nSeller cached: %t", sc.WorkshopID, c.Enabled, d.TokenType, d.IdentityCached)
	if client, ok := s.api.(interface{ TokenMetadata() wb.TokenMetadata }); ok {
		m := client.TokenMetadata()
		ro := "UNKNOWN"
		if m.ReadOnly != nil {
			ro = fmt.Sprint(*m.ReadOnly)
		}
		fmt.Fprintf(&b, "\nRead only: %s\nCategories (unverified metadata): %s\n", ro, strings.Join(m.Categories, ", "))
	}
	for _, source := range []wb.StockSource{wb.StockSeller, wb.StockWB} {
		var raw string
		info := wb.StockInfo{}
		capability := "UNKNOWN"
		_ = s.db.QueryRow(`SELECT info_json FROM marketplace_stock_runs WHERE workshop_id=? AND connection_id=? AND source=? ORDER BY id DESC LIMIT 1`, sc.WorkshopID, c.ID, source).Scan(&raw)
		_ = json.Unmarshal([]byte(raw), &info)
		if info.HTTPStatus == 200 {
			capability = "AVAILABLE"
		}
		if info.HTTPStatus == 403 {
			capability = "UNAVAILABLE"
		}
		if source == wb.StockWB && d.TokenType == "BASE" {
			capability = "UNAVAILABLE (docs: PERSONAL/SERVICE + Analytics)"
		}
		fmt.Fprintf(&b, "%s capability: %s; last status=%s HTTP=%d\n", source, capability, info.Status, info.HTTPStatus)
	}
	if d.IdentityCached {
		fmt.Fprintf(&b, "; fetched: %s", d.IdentityFetchedAt.UTC().Format(time.RFC3339))
	}
	b.WriteString("\nHealth: последние ответы API; heartbeat не выполняется.\n")
	for _, r := range d.Rates {
		if r.Blocked {
			fmt.Fprintf(&b, "%s: BLOCKED LOCALLY until %s (%s)\n", r.Group, r.RetryAt.UTC().Format(time.RFC3339), r.Source)
		} else {
			fmt.Fprintf(&b, "%s: available по локальному limiter\n", r.Group)
		}
	}
	rows, e := s.db.Query(`SELECT record_json FROM wb_request_trace WHERE workshop_id=? ORDER BY id DESC LIMIT 8`, sc.WorkshopID)
	if e != nil {
		return "", ErrStorage
	}
	defer rows.Close()
	b.WriteString("Последние события (HTTP=0 — ответа нет; sent=false — запрос не отправлен):\n")
	for rows.Next() {
		var raw string
		var r wb.RequestTrace
		if rows.Scan(&raw) != nil || json.Unmarshal([]byte(raw), &r) != nil {
			return "", ErrStorage
		}
		fmt.Fprintf(&b, "%s %s %s → %s HTTP=%d\n", r.Timestamp.UTC().Format("15:04:05Z"), r.WBMethod, r.Caller, r.Result, r.HTTPStatus)
		fmt.Fprintf(&b, "%s %s%s sent=%t\n", r.Method, r.Host, r.Endpoint, r.RequestSent)
	}
	if rows.Err() != nil {
		return "", ErrStorage
	}
	return b.String(), nil
}
