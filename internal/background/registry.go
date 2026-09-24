package background

import (
	"context"
	"sync"
	"workshop-agent/internal/auth"
)

type ExecutionResult struct {
	Status, ResultJSON, AggregateJSON, ErrorCode, Summary string
	CanNotify                                             func() bool
}
type Executor interface {
	Permissions() (read, manage auth.Permission)
	Validate(auth.Querier, int64, int64, string) (string, error)
	Check(auth.Querier, Job) error
	Execute(context.Context, Job, int64, string) ExecutionResult
}

// Register during bootstrap. Synchronization also makes test/plugin registration safe.
type Registry struct {
	mu    sync.RWMutex
	items map[string]Executor
}

func NewRegistry() *Registry { return &Registry{items: map[string]Executor{}} }
func (r *Registry) Register(kind string, ex Executor) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if kind == "" || ex == nil || r.items[kind] != nil {
		return ErrInput
	}
	r.items[kind] = ex
	return nil
}
func (r *Registry) Lookup(kind string) Executor {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.items[kind]
}
