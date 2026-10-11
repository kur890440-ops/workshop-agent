package llm

import (
	"context"
	"fmt"
	"workshop-agent/internal/stability"
)

// Call releases only the application state lock, never the conversation lease.
// All state reads/validation following inference run with the lock reacquired.
func Call(ctx context.Context, p GenerationProvider, prompt string, structured bool, budget int) (raw string, u *Usage, err error) {
	if scope := stability.ScopeFrom(ctx); scope != nil && scope.Yield != nil {
		resume := scope.Yield()
		defer func() {
			if e := resume(); e != nil {
				raw = ""
				err = e
			}
		}()
	}
	if e := ctx.Err(); e != nil {
		return "", nil, e
	}
	return p.Generate(ctx, prompt, structured, budget)
}

// Byte-bound admission is an explicitly labelled conservative estimate, not
// tokenizer output. Reserve includes the model chat template and special tokens.
func Preflight(ctx context.Context, prompt string, budget, templateReserve int) (int, error) {
	cfg := stability.Config(ctx)
	if budget > cfg.MaxOutput {
		budget = cfg.MaxOutput
	}
	estimate := len([]byte(prompt)) + templateReserve
	return checkBudget(ctx, estimate, budget, "conservative_utf8_byte_bound")
}

func checkBudget(ctx context.Context, estimate, budget int, method string) (int, error) {
	cfg := stability.Config(ctx)
	if budget > cfg.MaxOutput {
		budget = cfg.MaxOutput
	}
	if r := RunFrom(ctx); r != nil {
		r.TokenCountMethod = method
		r.InputEstimate = estimate
		r.OutputReserve = budget
		r.ContextLimit = cfg.MaxContext
	}
	if budget <= 0 || estimate+budget > cfg.MaxContext {
		return 0, &stability.Error{Code: "CONTEXT_LIMIT_EXCEEDED"}
	}
	return budget, nil
}

type ControlledProvider struct {
	Provider GenerationProvider
	Runtime  *stability.Runtime
	Local    bool
}

func (p *ControlledProvider) ModelName() string { return p.Provider.ModelName() }
func (p *ControlledProvider) Generate(ctx context.Context, prompt string, structured bool, budget int) (string, *Usage, error) {
	cfg := stability.Config(ctx)
	if stability.ScopeFrom(ctx) == nil {
		scope := &stability.Scope{Settings: p.Runtime.Settings.Get()}
		ctx = stability.WithScope(ctx, scope)
		cfg = scope.Settings
	}
	var e error
	if checker, ok := p.Provider.(interface {
		CheckPrompt(context.Context, string, int) (int, error)
	}); ok {
		budget, e = checker.CheckPrompt(ctx, prompt, budget)
	} else {
		budget, e = Preflight(ctx, prompt, budget, 512)
	}
	if e != nil {
		return "", nil, e
	}
	g := &p.Runtime.Cloud
	cap := cfg.Cloud
	if p.Local {
		g = &p.Runtime.Local
		cap = cfg.Local
	}
	scope := stability.ScopeFrom(ctx)
	if scope.State != nil {
		scope.State("QUEUED")
	}
	release, e := g.Acquire(ctx, scope.User, cap, cfg.Queue)
	if e != nil {
		return "", nil, e
	}
	defer release()
	if scope.State != nil {
		scope.State("RUNNING")
	}
	return p.Provider.Generate(ctx, prompt, structured, budget)
}
func ContextError(limit int) error {
	return fmt.Errorf("model context capacity %d: CONTEXT_LIMIT_EXCEEDED", limit)
}
