package wildberries

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// RateLimitError contains only closed operation/source names and a timestamp.
type RateLimitError struct {
	Operation string
	RetryAt   time.Time
	Source    string
}

func (e *RateLimitError) Error() string { return "WB: rate_limited" }
func (e *RateLimitError) Unwrap() error { return RateLimited }
func (e *RateLimitError) Message() string {
	operation := map[string]string{"common": "проверка кабинета (seller-info)", "analytics": "остатки WB", "content": "карточки", "marketplace": "маркетплейс"}[e.Operation]
	if operation == "" {
		operation = "запрос"
	}
	source := "локальная оценка"
	if e.Source == "wb_retry" || e.Source == "wb_reset" {
		source = "заголовки WB"
	}
	return fmt.Sprintf("WB: %s — повтор не раньше %s UTC (%s). Сохранённые данные: /wb.", operation, e.RetryAt.UTC().Format("2006-01-02 15:04:05"), source)
}

type CooldownStore interface {
	LoadCooldown(string) (RateLimitError, error)
	SaveCooldown(RateLimitError) error
}

// SetCooldownStore is a startup-only dependency injection; never swap during requests.
func (c *Client) SetCooldownStore(s CooldownStore) { c.store = s }
func ValidRateGroup(s string) bool {
	switch s {
	case "common", "analytics", "content", "marketplace", "prices":
		return true
	}
	return false
}
func ValidRateSource(s string) bool {
	switch s {
	case "wb_retry", "wb_reset", "local_backoff", "local_interval":
		return true
	}
	return false
}
func seconds(s string) (time.Duration, bool) {
	n, err := strconv.ParseUint(strings.TrimSpace(s), 10, 64)
	if err != nil || n > uint64((1<<63-1)/int64(time.Second)) {
		return 0, false
	}
	return time.Duration(n) * time.Second, true
}
func rateDelay(h http.Header, now time.Time, attempt int) (time.Duration, string) {
	best := time.Duration(0)
	found := false
	for _, name := range []string{"Retry-After", "X-Ratelimit-Retry"} {
		for _, value := range h.Values(name) {
			d, ok := seconds(value)
			if !ok && name == "Retry-After" {
				if date, e := http.ParseTime(value); e == nil {
					d = date.Sub(now)
					ok = d >= 0
				}
			}
			if ok {
				found = true
				if d > best {
					best = d
				}
			}
		}
	}
	source := "wb_retry"
	if !found {
		for _, value := range h.Values("X-Ratelimit-Reset") {
			if d, ok := seconds(value); ok {
				found = true
				source = "wb_reset"
				if d > best {
					best = d
				}
			}
		}
	}
	if !found {
		best = time.Duration(1<<attempt) * time.Second
		source = "local_backoff"
	}
	if best < time.Second {
		best = time.Second
	}
	return best, source
}
func (c *Client) saveCooldown(v RateLimitError) error {
	c.mu.Lock()
	old := c.cooldowns[v.Operation]
	if old.RetryAt.After(v.RetryAt) {
		v = old
	}
	c.cooldowns[v.Operation] = v
	if v.RetryAt.After(c.next[v.Operation]) {
		c.next[v.Operation] = v.RetryAt
	}
	c.mu.Unlock()
	if c.store != nil {
		if err := c.store.SaveCooldown(v); err != nil {
			return Unavailable
		}
	}
	return nil
}
func (c *Client) cooldown(group string) (RateLimitError, error) {
	c.mu.Lock()
	v := c.cooldowns[group]
	c.mu.Unlock()
	if c.store != nil {
		saved, err := c.store.LoadCooldown(group)
		if err != nil {
			return v, Unavailable
		}
		if saved.RetryAt.After(v.RetryAt) {
			v = saved
		}
	}
	return v, nil
}
func (c *Client) waitRate(ctx context.Context, group string) error {
	for {
		if err := guard(ctx); err != nil {
			return err
		}
		if ctx.Err() != nil {
			return Cancelled
		}
		saved, err := c.cooldown(group)
		if err != nil {
			return err
		}
		if saved.RetryAt.After(c.clock()) {
			// Server cooldowns are reported immediately; local page pacing may wait.
			if saved.Source != "local_interval" || saved.RetryAt.Sub(c.clock()) > 30*time.Second {
				return &saved
			}
		}
		c.mu.Lock()
		if saved.RetryAt.After(c.next[group]) {
			c.next[group] = saved.RetryAt
		}
		until := c.next[group]
		d := until.Sub(c.clock())
		if d <= 0 {
			next := c.clock().Add(c.intervals[group])
			c.next[group] = next
			c.mu.Unlock()
			if c.intervals[group] > 0 {
				if err := c.saveCooldown(RateLimitError{group, next, "local_interval"}); err != nil {
					return err
				}
			}
			return guard(ctx)
		}
		c.mu.Unlock()
		if d > 30*time.Second {
			return &RateLimitError{group, until, "local_interval"}
		}
		if err := c.sleep(ctx, d); err != nil {
			return err
		}
	}
}
