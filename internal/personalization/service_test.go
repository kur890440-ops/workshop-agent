package personalization_test

import (
	"path/filepath"
	"strings"
	"testing"
	"workshop-agent/internal/personalization"
	"workshop-agent/internal/workshops"
)

func TestProfileDefaultsPersistenceIsolationAndWorkshopSwitch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profile.db")
	ws := workshops.NewService(path)
	defer ws.Close()
	u, err := ws.UpsertUser(123, "", "A", "")
	if err != nil {
		t.Fatal(err)
	}
	v, err := ws.UpsertUser(124, "", "B", "")
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if err = ws.DB().QueryRow("SELECT COUNT(*) FROM user_preferences WHERE user_id=?", u).Scan(&n); err != nil || n != 1 {
		t.Fatal("no default profile", n, err)
	}
	p := personalization.New(ws.DB()).ForUser(u)
	def, err := p.GetProfile()
	if err != nil || def.Style != "neutral" || def.Detail != "normal" || def.Language != "ru" || def.SummaryFirst {
		t.Fatal(def, err)
	}
	if err = p.UpdatePreference("response_style", "concise", "test"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"A", "B"} {
		if _, err = ws.CreateOwnedWorkshop(u, name); err != nil {
			t.Fatal(err)
		}
		got, err := p.GetProfile()
		if err != nil || got.Detail != "brief" {
			t.Fatal(got, err)
		}
	}
	other, _ := personalization.New(ws.DB()).ForUser(v).GetProfile()
	if other.Detail != "normal" {
		t.Fatal("profile leaked")
	}
	reopened := workshops.NewService(path)
	defer reopened.Close()
	got, err := personalization.New(reopened.DB()).ForUser(u).GetProfile()
	if err != nil || got.Detail != "brief" {
		t.Fatal("restart lost profile", got, err)
	}
	if err = p.UpdatePreference("permissions", "OWNER", "test"); err == nil {
		t.Fatal("profile changed authorization")
	}
	if err = p.UpdatePreference("stock", "100", "test"); err == nil {
		t.Fatal("profile stored domain data")
	}
}
func TestResolvePrioritiesTemporaryPersistentAndTrace(t *testing.T) {
	ws := workshops.NewService(filepath.Join(t.TempDir(), "p.db"))
	defer ws.Close()
	u, err := ws.UpsertUser(123, "", "A", "")
	if err != nil {
		t.Fatal(err)
	}
	s := personalization.New(ws.DB()).ForUser(u)
	if err = s.UpdatePreference("response_style", "concise", "test"); err != nil {
		t.Fatal(err)
	}
	r, err := s.ResolveProfile("В этот раз объясни подробно.", nil)
	if err != nil || r.Applied.Detail != "detailed" || r.Stored.Detail != "brief" {
		t.Fatal(r, err)
	}
	next, _ := s.ResolveProfile("Обычный запрос", nil)
	if next.Applied.Detail != "brief" {
		t.Fatal("override persisted")
	}
	task, _ := s.ResolveProfile("Обычный запрос", map[string]string{"response_style": "detailed"})
	if task.Applied.Detail != "detailed" {
		t.Fatal("task below profile")
	}
	explicit, _ := s.ResolveProfile("Ответь коротко", map[string]string{"response_style": "detailed"})
	if explicit.Applied.Detail != "brief" {
		t.Fatal("explicit below task")
	}
	k, v, ok := personalization.PersistentIntent("Теперь всегда отвечай подробно.")
	if !ok {
		t.Fatal("missing persistent intent")
	}
	if err = s.UpdatePreference(k, v, "explicit"); err != nil {
		t.Fatal(err)
	}
	next, _ = s.ResolveProfile("Обычный запрос", nil)
	if next.Applied.Detail != "detailed" {
		t.Fatal("persistent update lost")
	}
	if err = s.SaveTrace(0, next); err != nil {
		t.Fatal(err)
	}
	trace, err := s.LastTrace()
	if err != nil || trace.Applied.Detail != "detailed" || !strings.Contains(trace.Context, "[USER PROFILE]") {
		t.Fatal(trace, err)
	}
	if _, _, ok = personalization.PersistentIntent("В этой мастерской перед производством всегда проверяем остатки"); ok {
		t.Fatal("workshop rule became profile")
	}
	if err = s.Reset(); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetProfile()
	if got.Detail != "normal" {
		t.Fatal("reset")
	}
}
