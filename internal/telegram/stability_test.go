package telegram

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
	"workshop-agent/internal/auth"
	"workshop-agent/internal/llm"
	"workshop-agent/internal/message"
	"workshop-agent/internal/stability"
)

func installRuntime(t *testing.T, h *harness) *stability.Runtime {
	t.Helper()
	s, e := stability.NewStore(h.bot.WS.DB(), stability.Defaults())
	if e != nil {
		t.Fatal(e)
	}
	r := stability.New(s)
	h.bot.EnableLimits(r)
	t.Cleanup(r.Wait)
	return r
}
func TestLimitsAdminDeterministicAndSharedBudget(t *testing.T) {
	h, local, _, w := hybridFixture(t)
	r := installRuntime(t, h)
	web := connectWeb(t, h)
	before := local.calls
	h.message(t, 900001, "/limits")
	requireAnswer(t, h, "Rate limit: 20/min")
	h.message(t, 900001, "/limits set rate 21")
	if r.Settings.Get().Rate != 21 || local.calls != before {
		t.Fatal("admin command used LLM or ignored value")
	}
	u, e := auth.ExistingTelegramUser(h.bot.WS.DB(), 900001)
	if e != nil {
		t.Fatal(e)
	}
	if e = r.Settings.Change(context.Background(), u, w, "rate", 20); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 19; i++ {
		if e = r.Charge(u, false); e != nil {
			t.Fatal(e)
		}
	}
	if e = h.bot.submitTelegram(context.Background(), &telegramMessage{Text: "what is PETG", Chat: telegramChat{ID: 900001, Type: "private"}, From: telegramUser{ID: 900001}}); e != nil {
		t.Fatal(e)
	}
	r.Wait()
	if e = web.service.Runtime.Charge(u, false); stability.Code(e) != "RATE_LIMIT_EXCEEDED" {
		t.Fatal("web does not share telegram budget", e)
	}
	if _, e = h.bot.WS.DB().Exec("UPDATE workshop_members SET role='VIEWER' WHERE user_id=? AND workshop_id=?", u, w); e != nil {
		t.Fatal(e)
	}
	m := &telegramMessage{Text: "/limits set rate 99", Chat: telegramChat{ID: 900001, Type: "private"}, From: telegramUser{ID: 900001}}
	if _, e = h.bot.limitsCommand(context.Background(), m); !errors.Is(e, auth.ErrDenied) {
		t.Fatal("viewer changed global config", e)
	}
	if r.Settings.Get().Rate != 20 {
		t.Fatal("unauthorized write")
	}
	m.Input = &message.Inbound{}
	if _, e = h.bot.limitsCommand(context.Background(), m); !errors.Is(e, auth.ErrDenied) {
		t.Fatal("web changed global config")
	}
}

type blockingLocal struct {
	mu          sync.Mutex
	active, max int
	entered     chan struct{}
	once        sync.Once
}

func (p *blockingLocal) ModelName() string { return "synthetic" }
func (p *blockingLocal) Generate(ctx context.Context, _ string, _ bool, _ int) (string, *llm.Usage, error) {
	p.mu.Lock()
	p.active++
	if p.active > p.max {
		p.max = p.active
	}
	p.mu.Unlock()
	defer func() { p.mu.Lock(); p.active--; p.mu.Unlock() }()
	p.once.Do(func() { close(p.entered) })
	<-ctx.Done()
	return "", nil, ctx.Err()
}
func TestInferenceYieldsGlobalStateLockAndCancellation(t *testing.T) {
	h, _, _, _ := hybridFixture(t)
	provider := &blockingLocal{entered: make(chan struct{})}
	h.bot.Hybrid.Local = provider
	r := installRuntime(t, h)
	web := connectWeb(t, h)
	ss, e := web.service.Authenticate(web.cookie.Value)
	if e != nil {
		t.Fatal(e)
	}
	job, e := r.Submit(context.Background(), ss.UserID, -ss.ID, "cancel-test", false, func(ctx context.Context) error {
		return h.bot.HandleInbound(ctx, message.Inbound{InputType: "WEB", UserID: ss.UserID, TelegramUserID: ss.TelegramUserID, WebSessionID: ss.ID, ConversationID: -ss.ID, Text: "general question"})
	}, nil)
	if e != nil {
		t.Fatal(e)
	}
	select {
	case <-provider.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("provider not entered")
	}
	done := make(chan error, 1)
	go func() {
		done <- h.bot.handleMessageContext(context.Background(), &telegramMessage{Text: "/limits", Chat: telegramChat{ID: 900001, Type: "private"}, From: telegramUser{ID: 900001}})
	}()
	select {
	case e = <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("global mutex held during inference")
	}
	job.Cancel()
	finished := make(chan struct{})
	go func() { job.Wait(); close(finished) }()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("provider cancellation lost")
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if provider.active != 0 || provider.max != 1 {
		t.Fatal(provider.active, provider.max)
	}
}
func TestHistoryReductionDiagnostic(t *testing.T) {
	h, _, _, _ := hybridFixture(t)
	h.message(t, 900001, strings.Repeat("x", 700))
	h.message(t, 900001, "question")
	var metadata string
	if e := h.bot.WS.DB().QueryRow("SELECT metadata FROM hybrid_llm_runs ORDER BY rowid DESC LIMIT 1").Scan(&metadata); e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(metadata, "history_reduced") {
		t.Fatal("missing reduction diagnostic")
	}
}
