package marketplace

import (
	"encoding/json"
	wb "workshop-agent/internal/marketplace/wildberries"
)

func (d cooldownDB) SaveObservation(o wb.RateObservation) error {
	_, e := d.db.Exec(`INSERT INTO wb_rate_observations(rate_group,received_at_ms,endpoint,http_status,remaining,retry_seconds,reset_seconds,limit_value) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(rate_group) DO UPDATE SET received_at_ms=excluded.received_at_ms,endpoint=excluded.endpoint,http_status=excluded.http_status,remaining=excluded.remaining,retry_seconds=excluded.retry_seconds,reset_seconds=excluded.reset_seconds,limit_value=excluded.limit_value`, o.Group, o.ReceivedAt.UnixMilli(), o.Endpoint, o.HTTPStatus, o.Remaining, o.RetrySeconds, o.ResetSeconds, o.Limit)
	return e
}
func (d cooldownDB) AppendTrace(r wb.RequestTrace) error {
	raw, e := json.Marshal(r)
	if e != nil {
		return ErrStorage
	}
	tx, e := d.db.Begin()
	if e != nil {
		return ErrStorage
	}
	defer tx.Rollback()
	if _, e = tx.Exec(`INSERT INTO wb_request_trace(workshop_id,record_json) VALUES(?,?)`, r.WorkshopID, string(raw)); e != nil {
		return ErrStorage
	}
	if r.Result == "WB_429" && r.HTTPStatus == 429 && r.RequestSent {
		rows, err := tx.Query(`SELECT record_json FROM wb_request_trace WHERE workshop_id=? ORDER BY id DESC LIMIT 51`, r.WorkshopID)
		if err != nil {
			return ErrStorage
		}
		history := []json.RawMessage{}
		for rows.Next() {
			var record string
			if err = rows.Scan(&record); err != nil {
				rows.Close()
				return ErrStorage
			}
			history = append(history, json.RawMessage(record))
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return ErrStorage
		}
		snapshot, err := json.Marshal(history)
		if err != nil {
			return ErrStorage
		}
		if _, err = tx.Exec(`INSERT INTO wb_rate_incidents(workshop_id,created_at,history_json) VALUES(?,?,?)`, r.WorkshopID, r.Timestamp.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"), string(snapshot)); err != nil {
			return ErrStorage
		}
		if _, err = tx.Exec(`DELETE FROM wb_rate_incidents WHERE workshop_id=? AND id NOT IN (SELECT id FROM wb_rate_incidents WHERE workshop_id=? ORDER BY id DESC LIMIT 10)`, r.WorkshopID, r.WorkshopID); err != nil {
			return ErrStorage
		}
	}
	if _, e = tx.Exec(`DELETE FROM wb_request_trace WHERE id NOT IN (SELECT id FROM wb_request_trace ORDER BY id DESC LIMIT 100)`); e != nil {
		return ErrStorage
	}
	return tx.Commit()
}
