package stability

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Gate uses FIFO per user and round-robin between users. Only one pending
// entry per user reserves queue space for other users. Shrink never revokes holders.
type waiter struct {
	user    int64
	ready   chan struct{}
	granted bool
}
type Gate struct {
	Capacity func() int
	mu       sync.Mutex
	active   int
	pending  []*waiter
	last     int64
}

func (g *Gate) Acquire(ctx context.Context, user int64, limit, queue int) (func(), error) {
	g.mu.Lock()
	if g.Capacity != nil {
		limit = g.Capacity()
	}
	if ctx.Err() != nil {
		g.mu.Unlock()
		return nil, ctx.Err()
	}
	if g.active < limit && len(g.pending) == 0 {
		g.active++
		g.mu.Unlock()
		return func() { g.release(limit) }, nil
	}
	if len(g.pending) >= queue {
		g.mu.Unlock()
		return nil, &Error{Code: "QUEUE_FULL"}
	}
	for _, w := range g.pending {
		if w.user == user {
			g.mu.Unlock()
			return nil, &Error{Code: "USER_QUEUE_FULL"}
		}
	}
	w := &waiter{user: user, ready: make(chan struct{})}
	g.pending = append(g.pending, w)
	g.mu.Unlock()
	select {
	case <-w.ready:
		return func() { g.release(limit) }, nil
	case <-ctx.Done():
		g.mu.Lock()
		granted := w.granted
		if !granted {
			for i, x := range g.pending {
				if x == w {
					g.pending = append(g.pending[:i], g.pending[i+1:]...)
					break
				}
			}
		}
		g.mu.Unlock()
		if granted {
			g.release(limit)
		}
		return nil, ctx.Err()
	}
}
func (g *Gate) release(limit int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.active--
	g.dispatch(limit)
}
func (g *Gate) dispatch(limit int) {
	if g.Capacity != nil {
		limit = g.Capacity()
	}
	for len(g.pending) > 0 && g.active < limit {
		idx := 0
		for i, w := range g.pending {
			if w.user != g.last {
				idx = i
				break
			}
		}
		w := g.pending[idx]
		g.pending = append(g.pending[:idx], g.pending[idx+1:]...)
		g.last = w.user
		g.active++
		w.granted = true
		close(w.ready)
	}
}

type rateBucket struct {
	count int
	until time.Time
}
type Runtime struct {
	activeUsers  map[int64]int
	Settings     *Store
	Local, Cloud Gate
	mu           sync.Mutex
	rates        map[string]rateBucket
	pending      []*Job
	active       map[int64]bool
	last         int64
	running      int
	wg           sync.WaitGroup
}
type Job struct {
	ID             string
	User, Dialogue int64
	Config         Settings
	Context        context.Context
	Cancel         context.CancelFunc
	Run            func(context.Context) error
	OnState        func(string, error)
	done           chan struct{}
}

func New(s *Store) *Runtime {
	r := &Runtime{activeUsers: map[int64]int{}, Settings: s, rates: map[string]rateBucket{}, active: map[int64]bool{}}
	r.Local.Capacity = func() int { return s.Get().Local }
	r.Cloud.Capacity = func() int { return s.Get().Cloud }
	s.OnChange = func() {
		r.Local.mu.Lock()
		r.Local.dispatch(s.Get().Local)
		r.Local.mu.Unlock()
		r.Cloud.mu.Lock()
		r.Cloud.dispatch(s.Get().Cloud)
		r.Cloud.mu.Unlock()
		r.mu.Lock()
		r.dispatch()
		r.mu.Unlock()
	}
	return r
}
func (r *Runtime) Charge(user int64, confirmation bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.charge(user, confirmation)
}
func (r *Runtime) charge(user int64, confirmation bool) error {
	now := time.Now()
	for k, v := range r.rates {
		if !now.Before(v.until) {
			delete(r.rates, k)
		}
	}
	key := fmt.Sprintf("%d/%t", user, confirmation)
	b := r.rates[key]
	if b.until.IsZero() {
		b.until = now.Add(time.Minute)
	}
	limit := r.Settings.Get().Rate
	if confirmation {
		limit = 60
	}
	if b.count >= limit {
		return &Error{Code: "RATE_LIMIT_EXCEEDED", RetryAfter: int(time.Until(b.until).Seconds()) + 1}
	}
	b.count++
	r.rates[key] = b
	return nil
}

// Submit is synchronous admission. Accepted work is owned by parent (application
// lifetime for Web), not by the HTTP connection. onState must not call Runtime.
func (r *Runtime) Submit(parent context.Context, user, dialogue int64, id string, confirmation bool, run func(context.Context) error, state func(string, error)) (*Job, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cfg := r.Settings.Get()
	waiting := r.active[dialogue] || r.activeUsers[user] > 0 || r.running >= cfg.Local+cfg.Cloud
	if r.running+len(r.pending) >= cfg.Queue+1 {
		return nil, &Error{Code: "QUEUE_FULL"}
	}
	if waiting {
		if len(r.pending) >= cfg.Queue {
			return nil, &Error{Code: "QUEUE_FULL"}
		}
		for _, p := range r.pending {
			if p.User == user {
				return nil, &Error{Code: "USER_QUEUE_FULL"}
			}
		}
	}
	if e := r.charge(user, confirmation); e != nil {
		return nil, e
	}
	ctx, cancel := context.WithTimeout(parent, time.Duration(cfg.Timeout)*time.Second)
	j := &Job{ID: id, User: user, Dialogue: dialogue, Config: cfg, Context: ctx, Cancel: cancel, Run: run, OnState: state, done: make(chan struct{})}
	r.wg.Add(1)
	if state != nil {
		state("QUEUED", nil)
	}
	r.pending = append(r.pending, j)
	r.dispatch()
	go func() {
		select {
		case <-ctx.Done():
			r.mu.Lock()
			for i, p := range r.pending {
				if p == j {
					r.pending = append(r.pending[:i], r.pending[i+1:]...)
					r.finish(j, ctx.Err())
					break
				}
			}
			r.mu.Unlock()
		case <-j.done:
		}
	}()
	return j, nil
}
func (r *Runtime) dispatch() {
	for r.running < r.Settings.Get().Local+r.Settings.Get().Cloud {
		idx := -1
		for i, j := range r.pending {
			if !r.active[j.Dialogue] && r.activeUsers[j.User] == 0 && (idx < 0 || j.User != r.last) {
				idx = i
				if j.User != r.last {
					break
				}
			}
		}
		if idx < 0 {
			return
		}
		j := r.pending[idx]
		r.pending = append(r.pending[:idx], r.pending[idx+1:]...)
		r.running++
		r.active[j.Dialogue] = true
		r.activeUsers[j.User]++
		r.last = j.User
		go func() {
			scope := &Scope{User: j.User, Settings: j.Config, ID: j.ID}
			scope.State = func(s string) {
				if j.OnState != nil {
					j.OnState(s, nil)
				}
			}
			ctx := WithScope(j.Context, scope)
			if j.OnState != nil {
				j.OnState("RUNNING", nil)
			}
			e := ctx.Err()
			if e == nil {
				e = j.Run(ctx)
			}
			if ctx.Err() != nil {
				e = ctx.Err()
			}
			r.mu.Lock()
			delete(r.active, j.Dialogue)
			r.activeUsers[j.User]--
			r.running--
			r.finish(j, e)
			r.dispatch()
			r.mu.Unlock()
		}()
	}
}
func (r *Runtime) finish(j *Job, e error) {
	status := "COMPLETED"
	if e != nil {
		status = "FAILED"
		if j.Context.Err() != nil {
			status = "CANCELLED"
		}
	}
	if j.OnState != nil {
		j.OnState(status, e)
	}
	j.Cancel()
	close(j.done)
	r.wg.Done()
}
func (j *Job) Wait()     { <-j.done }
func (r *Runtime) Wait() { r.wg.Wait() }
