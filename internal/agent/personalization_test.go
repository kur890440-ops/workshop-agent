package agent

import (
	"context"
	"strings"
	"testing"
	"workshop-agent/internal/auth"
	"workshop-agent/internal/llm"
	"workshop-agent/internal/memory"
	"workshop-agent/internal/personalization"
)

type profileModel struct{ prompts []string }

func (m *profileModel) ParseCommand(_ context.Context, p string) (*llm.StructuredCommand, *llm.Usage, error) {
	m.prompts = append(m.prompts, p)
	return &llm.StructuredCommand{Action: "clarification"}, nil, nil
}
func (m *profileModel) Complete(_ context.Context, p string) (string, *llm.Usage, error) {
	m.prompts = append(m.prompts, p)
	return "Модель получила профиль и доменные факты.", &llm.Usage{}, nil
}
func TestProfileInEveryRequestAndNewSession(t *testing.T) {
	a, u, w := day11(t)
	model := &profileModel{}
	a.LLM = model
	s := personalization.New(a.WS.DB()).ForUser(u)
	if err := s.UpdatePreference("response_style", "concise", "test"); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"Непонятный запрос один", "/session new", "Непонятный запрос два", "Покажи материалы мастерской и оцени текущее состояние."} {
		if _, _, err := a.HandleMessageForWorkshop(context.Background(), w, u, u, text); err != nil {
			t.Fatal(err)
		}
	}
	if len(model.prompts) != 3 {
		t.Fatal("unexpected LLM calls", len(model.prompts))
	}
	for _, p := range model.prompts {
		if strings.Count(p, "[USER PROFILE]") != 1 || !strings.Contains(p, "Detail level: brief") {
			t.Fatal("profile missing/duplicate", p)
		}
	}
	before, _ := s.GetProfile()
	if _, _, err := a.HandleMessageForWorkshop(context.Background(), w, u, u, "Подготовь отчет. В этот раз объясни подробно."); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(model.prompts[len(model.prompts)-1], "Detail level: detailed") {
		t.Fatal("override missing")
	}
	after, _ := s.GetProfile()
	if before.Detail != after.Detail {
		t.Fatal("override persisted")
	}
}
func TestProfileRenderingFactsAndPriority(t *testing.T) {
	a, u, w := day11(t)
	items, err := a.Inv.ForUser(u).ListMaterials(w)
	if err != nil {
		t.Fatal(err)
	}
	pa := personalization.Default(u)
	pa.Detail = "brief"
	pa.Format = "list"
	pa.SummaryFirst = true
	pb := pa
	pb.Detail = "detailed"
	pb.Format = "table"
	pb.SummaryFirst = false
	aa := RenderMaterialReport(items, pa)
	bb := RenderMaterialReport(items, pb)
	if !strings.HasPrefix(aa, "Краткий итог:") || !strings.Contains(aa, "- ") || !strings.Contains(bb, " | ") || aa == bb {
		t.Fatal(aa, bb)
	}
	for _, p := range items {
		if !strings.Contains(aa, p["name"].(string)) || !strings.Contains(bb, p["name"].(string)) {
			t.Fatal("lost fact")
		}
	}
	svc := personalization.New(a.WS.DB()).ForUser(u)
	if err = svc.UpdatePreference("response_style", "concise", "test"); err != nil {
		t.Fatal(err)
	}
	if err = a.Memory.ForUser(u).SaveLongTermMemory(memory.Scope{UserID: u, WorkshopID: w}, memory.LongTerm{Type: "USER_PREFERENCE", ScopeType: "user", Key: "response_style", Value: "detailed"}); err != nil {
		t.Fatal(err)
	}
	p, _ := svc.GetProfile()
	if p.Detail != "detailed" {
		t.Fatal("Day11 not connected")
	}
}
func TestProfileCannotAuthorizeDomainMutation(t *testing.T) {
	a, u, w := day11(t)
	v, err := a.WS.UpsertUser(8822, "", "Employee", "")
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := a.WS.CreateInvite(u, w, auth.Employee, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.WS.AcceptInvite(v, token); err != nil {
		t.Fatal(err)
	}
	if err = personalization.New(a.WS.DB()).ForUser(v).UpdatePreference("confirmation_level", "minimal_confirmation", "test"); err != nil {
		t.Fatal(err)
	}
	if _, _, err = a.HandleMessageForWorkshop(context.Background(), w, v, 8822, "Теперь всегда клади в Набор 4 кисти."); err == nil {
		t.Fatal("profile bypassed bom.write")
	}
}
