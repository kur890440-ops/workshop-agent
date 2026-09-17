package agent

import (
	"context"
	"path/filepath"
	"testing"
	"workshop-agent/internal/auth"
	"workshop-agent/internal/inventory"
	"workshop-agent/internal/llm"
	"workshop-agent/internal/products"
	"workshop-agent/internal/workshops"
)

type countingLLM struct{ calls int }

func (m *countingLLM) ParseCommand(context.Context, string) (*llm.StructuredCommand, *llm.Usage, error) {
	m.calls++
	return &llm.StructuredCommand{Action: "record_production", Product: "Product"}, &llm.Usage{}, nil
}
func TestAgentAuthorizationBeforeLLMAndDomainActions(t *testing.T) {
	p := filepath.Join(t.TempDir(), "agent.db")
	ws := workshops.NewService(p)
	defer ws.Close()
	inv := inventory.NewService(p)
	defer inv.Close()
	prod := products.NewBOMService(p)
	defer prod.Close()
	owner, err := ws.UpsertUser(100001, "", "Owner", "")
	if err != nil {
		t.Fatal(err)
	}
	w, err := ws.CreateOwnedWorkshop(owner, "A")
	if err != nil {
		t.Fatal(err)
	}
	viewer, err := ws.UpsertUser(100002, "", "Viewer", "")
	if err != nil {
		t.Fatal(err)
	}
	mock := &countingLLM{}
	a := NewWorkshopAgent(mock, ws, inv, prod)
	if _, _, err := a.HandleMessageForWorkshop(context.Background(), w, viewer, 100002, "do something"); err == nil || mock.calls != 0 {
		t.Fatal("unauthorized caller reached LLM")
	}
	_, token, err := ws.CreateInvite(owner, w, auth.Viewer, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ws.AcceptInvite(viewer, token); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.HandleMessageForWorkshop(context.Background(), w, viewer, 100002, "record production"); err == nil {
		t.Fatal("LLM bypassed production permission")
	}
	if err := a.SaveSession(w, 100002, 999, "context", ""); err == nil {
		t.Fatal("session write bypassed membership")
	}
}
