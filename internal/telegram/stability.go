package telegram

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"workshop-agent/internal/auth"
	"workshop-agent/internal/llm"
	"workshop-agent/internal/stability"
)

// enterApplication guards hybridContext, hybridPending, materialLists,
// completions and navigation state. Inference temporarily yields this lock.
// Runtime retains the dialogue lease while it is yielded.
func (b *Bot) enterApplication(ctx context.Context, user int64) func() {
	b.applicationMu.Lock()
	scope := stability.ScopeFrom(ctx)
	if scope == nil {
		scope = &stability.Scope{User: user, Settings: stability.Defaults()}
		ctx = stability.WithScope(ctx, scope)
	}
	b.hybridContext = ctx
	scope.Yield = func() func() error {
		saved := b.hybridContext
		w, _ := b.WS.ActiveWorkshop(user)
		var role string
		_ = b.WS.DB().QueryRow("SELECT role FROM workshop_members WHERE user_id=? AND workshop_id=?", user, w).Scan(&role)
		b.hybridContext = nil
		b.applicationMu.Unlock()
		return func() error {
			b.applicationMu.Lock()
			b.hybridContext = saved
			if e := ctx.Err(); e != nil {
				return e
			}
			if scope.WebSession > 0 {
				var n int
				e := b.WS.DB().QueryRow("SELECT count(*) FROM web_sessions WHERE id=? AND user_id=? AND revoked_at IS NULL AND expires_at>unixepoch()", scope.WebSession, user).Scan(&n)
				if e != nil {
					return e
				}
				if n != 1 {
					return auth.ErrDenied
				}
			}
			if w > 0 {
				if e := auth.Require(b.WS.DB(), user, w, auth.WorkshopRead); e != nil {
					return e
				}
				active, e := b.WS.ActiveWorkshop(user)
				if e != nil || active != w {
					return auth.ErrDenied
				}
				var nowRole string
				_ = b.WS.DB().QueryRow("SELECT role FROM workshop_members WHERE user_id=? AND workshop_id=?", user, w).Scan(&nowRole)
				if nowRole != role {
					return auth.ErrDenied
				}
			}
			return nil
		}
	}
	return func() { b.hybridContext = nil; b.applicationMu.Unlock() }
}
func (b *Bot) limitsCommand(ctx context.Context, m *telegramMessage) (bool, error) {
	parts := strings.Fields(m.Text)
	if len(parts) == 0 || parts[0] != "/limits" {
		return false, nil
	}
	if m.Input != nil || m.Chat.Type != "private" || m.Chat.ID != m.From.ID {
		return true, auth.ErrDenied
	}
	if b.Runtime == nil {
		return true, errors.New("limits unavailable")
	}
	user, e := auth.ExistingTelegramUser(b.WS.DB(), m.From.ID)
	if e != nil {
		return true, e
	}
	w, e := b.WS.ActiveWorkshop(user)
	if e != nil {
		return true, e
	}
	if e = auth.Require(b.WS.DB(), user, w, auth.WorkshopManage); e != nil {
		return true, e
	}
	if len(parts) > 1 {
		if len(parts) != 4 || parts[1] != "set" {
			return true, errors.New("Используйте /limits set parameter value")
		}
		v, err := strconv.Atoi(parts[3])
		if err != nil {
			return true, errors.New("Значение должно быть целым")
		}
		if e = b.Runtime.Settings.Change(ctx, user, w, parts[2], v); e != nil {
			return true, e
		}
	}
	s := b.Runtime.Settings.Get()
	return true, b.sendMessage(m.Chat.ID, fmt.Sprintf("Лимиты процесса (OWNER/ADMIN активной мастерской)\nRate limit: %d/min\nLocal concurrency: %d\nCloud concurrency: %d\nQueue size: %d\nMax context: %d\nMax output: %d\nRequest timeout: %d s\nПодсчёт: токенизатор поддерживаемой модели + резерв; иначе консервативная оценка.\nОчередь: не более одного ожидающего запроса пользователя.", s.Rate, s.Local, s.Cloud, s.Queue, s.MaxContext, s.MaxOutput, s.Timeout))
}
func (b *Bot) submitTelegram(ctx context.Context, m *telegramMessage) error {
	if b.Runtime == nil {
		return b.handleMessageContext(ctx, m)
	}
	if strings.HasPrefix(strings.TrimSpace(m.Text), "/limits") || strings.HasPrefix(strings.TrimSpace(m.Text), "/login") || strings.TrimSpace(m.Text) == "/start" {
		return b.handleMessageContext(ctx, m)
	}
	user, e := auth.ExistingTelegramUser(b.WS.DB(), m.From.ID)
	if e != nil {
		return b.handleMessageContext(ctx, m)
	}
	_, e = b.Runtime.Submit(ctx, user, m.Chat.ID, newHybridID(), false, func(c context.Context) error { return b.handleMessageContext(c, m) }, nil)
	if e != nil {
		return b.api("sendMessage", map[string]any{"chat_id": m.Chat.ID, "text": limitMessage(e)}, nil)
	}
	return nil
}
func limitMessage(e error) string {
	for _, prefix := range []string{"INVALID_LIMIT", "UNKNOWN_LIMIT", "MODEL_CAPACITY_UNVERIFIED", "MODEL_CONTEXT_UNSUPPORTED", "HARDWARE_CAPACITY_UNVERIFIED", "CLOUD_MODEL_CAPACITY_UNVERIFIED_OR_UNSUPPORTED", "Используйте /limits", "Значение должно"} {
		if strings.HasPrefix(e.Error(), prefix) {
			return e.Error()
		}
	}
	var x *stability.Error
	if errors.As(e, &x) {
		if x.RetryAfter > 0 {
			return fmt.Sprintf("Превышен лимит. Повторите через %d секунд.", x.RetryAfter)
		}
		if x.Code == "QUEUE_FULL" || x.Code == "USER_QUEUE_FULL" {
			return "Очередь заполнена или у вас уже есть ожидающий запрос. Дождитесь ответа."
		}
		if x.Code == "CONTEXT_LIMIT_EXCEEDED" {
			return "CONTEXT_LIMIT_EXCEEDED: общий контекст (инструкции, история и вопрос) вместе с резервом ответа превышает лимит модели. Длина вашего сообщения может быть ни при чём."
		}
	}
	return publicError(e)
}
func (b *Bot) enableLimits(r *stability.Runtime) {
	b.Runtime = r
	if b.Hybrid != nil {
		if _, ok := b.Hybrid.Local.(*llm.ControlledProvider); !ok && b.Hybrid.Local != nil {
			b.Hybrid.Local = &llm.ControlledProvider{Provider: b.Hybrid.Local, Runtime: r, Local: true}
		}
		if _, ok := b.Hybrid.Cloud.(*llm.ControlledProvider); !ok && b.Hybrid.Cloud != nil {
			b.Hybrid.Cloud = &llm.ControlledProvider{Provider: b.Hybrid.Cloud, Runtime: r}
		}
	}
}
func (b *Bot) EnableLimits(r *stability.Runtime) { b.enableLimits(r) }

func (b *Bot) submitCallback(ctx context.Context, c *telegramCallback) error {
	if b.Runtime == nil {
		return b.handleCallbackContext(ctx, c)
	}
	if c == nil || c.Message == nil {
		return nil
	}
	if c.Message.Chat.Type != "private" || c.Message.Chat.ID != c.From.ID {
		return auth.ErrDenied
	}
	u, e := auth.ExistingTelegramUser(b.WS.DB(), c.From.ID)
	if e != nil {
		return e
	}
	_, e = b.Runtime.Submit(ctx, u, c.Message.Chat.ID, newHybridID(), true, func(ctx context.Context) error { return b.handleCallbackContext(ctx, c) }, nil)
	if e != nil {
		return b.api("sendMessage", map[string]any{"chat_id": c.Message.Chat.ID, "text": limitMessage(e)}, nil)
	}
	return nil
}

type reportedError struct{ err error }

func (e *reportedError) Error() string { return e.err.Error() }
func (e *reportedError) Unwrap() error { return e.err }
func (b *Bot) reportError(chat int64, text string, e error) error {
	_ = b.sendMessage(chat, text)
	return &reportedError{e}
}

func (b *Bot) requestOwned() bool {
	s := stability.ScopeFrom(b.llmContext())
	return s != nil && s.ID != ""
}
func (b *Bot) network(ctx context.Context, fn func(context.Context) error) (err error) {
	if s := stability.ScopeFrom(ctx); s != nil && s.Yield != nil {
		resume := s.Yield()
		defer func() {
			if e := resume(); e != nil {
				err = e
			}
		}()
	}
	if e := ctx.Err(); e != nil {
		return e
	}
	return fn(ctx)
}
