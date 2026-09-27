package marketplace

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"workshop-agent/internal/auth"
	wb "workshop-agent/internal/marketplace/wildberries"
)

// TraceHistoryText reads only persisted diagnostics, never WB API.
func (s *Service) TraceHistoryText(sc Scope, incident bool, offset int) (string, error) {
	if offset < 0 || offset > 100 {
		return "", ErrInput
	}
	if e := authorize(s.db, sc, auth.MarketplaceManage); e != nil {
		return "", e
	}
	if _, e := connection(s.db, sc, false); e != nil {
		return "", e
	}
	records := []wb.RequestTrace{}
	title := "WB history: последние события, от новых к старым."
	command := "history"
	if incident {
		var id int64
		var raw string
		e := s.db.QueryRow(`SELECT id,history_json FROM wb_rate_incidents WHERE workshop_id=? ORDER BY id DESC LIMIT 1`, sc.WorkshopID).Scan(&id, &raw)
		if errors.Is(e, sql.ErrNoRows) {
			return "Сохранённых HTTP429 пока нет.", nil
		}
		if e != nil || len(raw) > 1<<20 || json.Unmarshal([]byte(raw), &records) != nil {
			return "", ErrStorage
		}
		title = fmt.Sprintf("Последний HTTP429 #%d: событие и до 50 предшествующих; от новых к старым.", id)
		command = "incident"
	} else {
		rows, e := s.db.Query(`SELECT record_json FROM wb_request_trace WHERE workshop_id=? ORDER BY id DESC LIMIT 100`, sc.WorkshopID)
		if e != nil {
			return "", ErrStorage
		}
		defer rows.Close()
		for rows.Next() {
			var raw string
			var r wb.RequestTrace
			if rows.Scan(&raw) != nil || json.Unmarshal([]byte(raw), &r) != nil {
				return "", ErrStorage
			}
			records = append(records, r)
		}
		if rows.Err() != nil {
			return "", ErrStorage
		}
	}
	var b strings.Builder
	b.WriteString(title + "\n")
	if offset >= len(records) {
		b.WriteString("Событий на этой странице нет.")
		return b.String(), nil
	}
	end := min(offset+4, len(records))
	for _, r := range records[offset:end] {
		fmt.Fprintf(&b, "\n%s caller=%s\n%s https://%s%s\nGo=%s MCP=%s group=%s\nHTTP=%d result=%s sent=%t attempt=%d\n", r.Timestamp.UTC().Format(time.RFC3339), r.Caller, r.Method, r.Host, r.Endpoint, r.WBMethod, r.Tool, r.RateKey, r.HTTPStatus, r.Result, r.RequestSent, r.Attempt)
		number := func(v *int64) string {
			if v == nil {
				return "null"
			}
			return fmt.Sprint(*v)
		}
		fmt.Fprintf(&b, "limit=%s remaining=%s retry=%s reset=%s\n", number(r.Limit), number(r.Remaining), number(r.Retry), number(r.Reset))
		if !r.RequestStartedAt.IsZero() {
			fmt.Fprintf(&b, "started=%s finished=%s duration_ms=%d\n", r.RequestStartedAt.UTC().Format(time.RFC3339Nano), r.RequestFinishedAt.UTC().Format(time.RFC3339Nano), r.DurationMS)
		}
		if r.RetryAt != nil {
			fmt.Fprintf(&b, "retry_not_before=%s\n", r.RetryAt.UTC().Format(time.RFC3339))
		}
		if r.Detail != "" {
			fmt.Fprintf(&b, "detail=%s\n", r.Detail)
		}
	}
	if end < len(records) {
		fmt.Fprintf(&b, "\nДалее: /wb %s %d", command, end)
	}
	return b.String(), nil
}
