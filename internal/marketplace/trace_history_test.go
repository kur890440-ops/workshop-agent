package marketplace

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
	wb "workshop-agent/internal/marketplace/wildberries"
	"workshop-agent/internal/storage"
)

func TestIncidentHistoryPersistsBeyondRingAndRestart(t *testing.T) {
	f := setup(t)
	f.attach(t)
	d := cooldownDB{f.ws.DB()}
	at := time.Now().UTC()
	until := at.Add(24 * time.Hour).Truncate(time.Millisecond)
	if e := d.SaveCooldown(wb.RateLimitError{Operation: "common", RetryAt: until, Source: "wb_retry"}); e != nil {
		t.Fatal(e)
	}
	record := wb.RequestTrace{Timestamp: at, WorkshopID: f.sc.WorkshopID, Caller: "background:WB_DAILY_SYNC", Host: "common-api.wildberries.ru", Endpoint: "/api/v1/seller-info", Method: "GET", WBMethod: "Seller", Result: "SUCCESS", HTTPStatus: 200, RequestSent: true, Attempt: 1}
	for i := 0; i < 60; i++ {
		record.DurationMS = int64(i)
		if e := d.AppendTrace(record); e != nil {
			t.Fatal(e)
		}
	}
	record.Result = "WB_429"
	record.HTTPStatus = 429
	record.RetryAt = &until
	if e := d.AppendTrace(record); e != nil {
		t.Fatal(e)
	}
	record.Result = "BLOCKED_LOCALLY"
	record.HTTPStatus = 0
	record.RequestSent = false
	record.Attempt = 0
	for i := 0; i < 120; i++ {
		if e := d.AppendTrace(record); e != nil {
			t.Fatal(e)
		}
	}
	reopened, e := storage.New(f.path)
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close()
	var raw string
	if e = reopened.DB.QueryRow(`SELECT history_json FROM wb_rate_incidents WHERE workshop_id=?`, f.sc.WorkshopID).Scan(&raw); e != nil {
		t.Fatal(e)
	}
	var history []wb.RequestTrace
	if e = json.Unmarshal([]byte(raw), &history); e != nil {
		t.Fatal(e)
	}
	if len(history) != 51 || history[0].Result != "WB_429" || history[1].DurationMS != 59 || history[50].DurationMS != 10 {
		t.Fatal("incident history lost", len(history))
	}
	if count(t, reopened.DB, "wb_request_trace") != 100 || count(t, reopened.DB, "wb_rate_incidents") != 1 {
		t.Fatal("ring bounds or false incident")
	}
	saved, e := (cooldownDB{reopened.DB}).LoadCooldown("common")
	if e != nil || !saved.RetryAt.Equal(until) {
		t.Fatal("restart lost cooldown", e)
	}
	service, e := New(reopened.DB, f.api)
	if e != nil {
		t.Fatal(e)
	}
	defer service.Close()
	text, e := service.TraceHistoryText(f.sc, true, 0)
	if e != nil || !strings.Contains(text, "WB_429") || !strings.Contains(text, "/api/v1/seller-info") || !strings.Contains(text, "background:WB_DAILY_SYNC") {
		t.Fatal(e, text)
	}
	denied := f.sc
	denied.UserID += 1000
	if _, e = service.TraceHistoryText(denied, true, 0); e == nil {
		t.Fatal("unauthorized history")
	}
	denied = f.sc
	denied.WorkshopID += 1000
	if _, e = service.TraceHistoryText(denied, false, 0); e == nil {
		t.Fatal("cross-workshop history")
	}
	if _, e = service.TraceHistoryText(f.sc, true, -1); e == nil {
		t.Fatal("negative offset")
	}
	record.Result = "WB_429"
	record.HTTPStatus = 429
	record.RequestSent = true
	for i := 0; i < 12; i++ {
		if e = d.AppendTrace(record); e != nil {
			t.Fatal(e)
		}
	}
	if count(t, reopened.DB, "wb_rate_incidents") != 10 {
		t.Fatal("unbounded incident history")
	}
}
