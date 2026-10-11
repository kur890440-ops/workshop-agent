package stability

import (
	"context"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"workshop-agent/internal/workshops"
)

func fixture(t *testing.T) (*Store, int64, int64) {
	t.Helper()
	ws := workshops.NewService(filepath.Join(t.TempDir(), "test.db"))
	t.Cleanup(func() { ws.Close() })
	u, e := ws.UpsertUser(900001, "synthetic", "Synthetic", "")
	if e != nil {
		t.Fatal(e)
	}
	w, e := ws.CreateOwnedWorkshop(u, "Synthetic")
	if e != nil {
		t.Fatal(e)
	}
	s, e := NewStore(ws.DB(), Defaults())
	if e != nil {
		t.Fatal(e)
	}
	return s, u, w
}
func TestUserBudgetPersistenceAndAuthorization(t *testing.T) {
	s, u, w := fixture(t)
	r := New(s)
	for i := 0; i < 20; i++ {
		if e := r.Charge(u, false); e != nil {
			t.Fatal(i, e)
		}
	}
	if e := r.Charge(u, false); Code(e) != "RATE_LIMIT_EXCEEDED" {
		t.Fatal(e)
	}
	if e := r.Charge(u, true); e != nil {
		t.Fatal("confirmation charged as message", e)
	}
	if e := s.Change(context.Background(), u, w, "rate", 21); e != nil {
		t.Fatal(e)
	}
	if e := r.Charge(u, false); e != nil {
		t.Fatal("new setting not applied", e)
	}
	again, e := NewStore(s.DB, Defaults())
	if e != nil || again.Get().Rate != 21 {
		t.Fatal("not persistent", e)
	}
	if _, e = s.DB.Exec("UPDATE workshop_members SET role='VIEWER' WHERE user_id=?", u); e != nil {
		t.Fatal(e)
	}
	if e = s.Change(context.Background(), u, w, "rate", 99); e == nil {
		t.Fatal("viewer changed settings")
	}
	var n int
	s.DB.QueryRow("SELECT count(*) FROM runtime_limit_audit WHERE actor=? AND old_value=20 AND new_value=21", u).Scan(&n)
	if n != 1 {
		t.Fatal("audit", n)
	}
	for k, v := range map[string]int{"rate": 0, "max_context": 99999, "max_output": 99999, "timeout": 0} {
		if e := s.Change(context.Background(), u, w, k, v); e == nil {
			t.Fatal(k)
		}
	}
}
func TestGateQueueFairnessCancellationAndShrink(t *testing.T) {
	var cap atomic.Int32
	cap.Store(1)
	g := Gate{Capacity: func() int { return int(cap.Load()) }}
	ctx := context.Background()
	release, e := g.Acquire(ctx, 1, 1, 5)
	if e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	order := make(chan int64, 5)
	for i := int64(2); i <= 6; i++ {
		wg.Add(1)
		go func(u int64) {
			defer wg.Done()
			free, e := g.Acquire(ctx, u, 1, 5)
			if e != nil {
				t.Error(e)
				return
			}
			order <- u
			free()
		}(i)
	}
	deadline := time.Now().Add(time.Second)
	for {
		g.mu.Lock()
		n := len(g.pending)
		g.mu.Unlock()
		if n == 5 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("queue not populated")
		}
		time.Sleep(time.Millisecond)
	}
	if _, e := g.Acquire(ctx, 8, 1, 5); Code(e) != "QUEUE_FULL" {
		t.Fatal(e)
	}
	release()
	wg.Wait()
	close(order)
	seen := map[int64]bool{}
	for u := range order {
		seen[u] = true
	}
	if len(seen) != 5 {
		t.Fatal("starvation")
	}
	release, e = g.Acquire(ctx, 1, 1, 5)
	if e != nil {
		t.Fatal(e)
	}
	cancelctx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { _, e := g.Acquire(cancelctx, 2, 1, 5); done <- e }()
	cancel()
	if e = <-done; e != context.Canceled {
		t.Fatal(e)
	}
	release()
	cap.Store(2)
	a, _ := g.Acquire(ctx, 1, 2, 5)
	b, _ := g.Acquire(ctx, 2, 2, 5)
	cap.Store(1)
	a()
	g.mu.Lock()
	active := g.active
	g.mu.Unlock()
	if active != 1 {
		t.Fatal(active)
	}
	b()
	t.Log("1 running + 5 waiting; overflow rejected; all users served; cancellation releases wait; shrink drains holders")
}
func TestRuntimeDialogOrderQueueAndCancel(t *testing.T) {
	s, _, _ := fixture(t)
	r := New(s)
	block := make(chan struct{})
	started := make(chan struct{})
	first, e := r.Submit(context.Background(), 1, 10, "first", false, func(context.Context) error { close(started); <-block; return nil }, nil)
	if e != nil {
		t.Fatal(e)
	}
	<-started
	ran := false
	second, e := r.Submit(context.Background(), 1, 10, "second", false, func(context.Context) error { ran = true; return nil }, nil)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = r.Submit(context.Background(), 1, 10, "third", false, func(context.Context) error { return nil }, nil); Code(e) != "USER_QUEUE_FULL" {
		t.Fatal(e)
	}
	second.Cancel()
	second.Wait()
	close(block)
	first.Wait()
	if ran {
		t.Fatal("cancelled waiter ran")
	}
}

func TestRuntimeConcurrencyChangeWakesWaiters(t *testing.T) {
	s, u, w := fixture(t)
	r := New(s)
	first, e := r.Cloud.Acquire(context.Background(), 1, 2, 5)
	if e != nil {
		t.Fatal(e)
	}
	second, e := r.Cloud.Acquire(context.Background(), 2, 2, 5)
	if e != nil {
		t.Fatal(e)
	}
	defer first()
	defer second()
	ready := make(chan func(), 1)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() {
		release, e := r.Cloud.Acquire(ctx, 3, 2, 5)
		if e == nil {
			ready <- release
		}
	}()
	if e = s.Change(context.Background(), u, w, "cloud_concurrency", 3); e != nil {
		t.Fatal(e)
	}
	select {
	case release := <-ready:
		release()
	case <-ctx.Done():
		t.Fatal("runtime increase did not wake waiter")
	}
}
