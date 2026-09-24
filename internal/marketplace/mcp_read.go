package marketplace

import (
	"context"
	"database/sql"
	"time"

	"workshop-agent/internal/auth"
	wb "workshop-agent/internal/marketplace/wildberries"
)

// MCPCooldowns shares existing rate deadlines with the locally trusted server.
// The server must open an existing database; it must not run app migrations.
func MCPCooldowns(db *sql.DB) wb.CooldownStore { return cooldownDB{db} }

// MCPRead reserves the same single-flight slot as ordinary WB synchronization.
// The callback performs MCP calls only, never direct WB HTTP. No transaction spans
// a network wait. Revocation cancels the request and suppresses late results.
func (s *Service) MCPRead(ctx context.Context, scope Scope, call func(context.Context, Connection) error) error {
	s.mu.Lock()
	if err := authorize(s.db, scope, auth.MarketplaceRead); err != nil {
		s.mu.Unlock()
		return err
	}
	c, err := connection(s.db, scope, true)
	if err != nil {
		s.mu.Unlock()
		return err
	}
	if c.SellerID == "" {
		s.mu.Unlock()
		return Error("Сначала проверьте привязанный кабинет: /wb check.")
	}
	if s.job != nil {
		s.mu.Unlock()
		return ErrBusy
	}
	ctx, cancel := context.WithTimeout(ctx, 110*time.Second)
	j := &job{scope: scope, revision: c.Revision, cancel: cancel, done: make(chan struct{})}
	s.job = j
	s.wg.Add(1)
	s.mu.Unlock()
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if s.check(scope, c.Revision) != nil {
					cancel()
					return
				}
			}
		}
	}()
	defer func() {
		cancel()
		<-watchDone
		s.mu.Lock()
		s.job = nil
		close(j.done)
		s.mu.Unlock()
		s.wg.Done()
	}()
	if err = s.check(scope, c.Revision); err != nil {
		return err
	}
	err = call(ctx, c)
	if denied := s.check(scope, c.Revision); denied != nil {
		return denied
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	result := "success"
	if err != nil {
		result = "failed"
	}
	// Fixed audit values; never include remote error text or API payloads.
	_, auditErr := s.db.Exec(`INSERT INTO audit_logs(workshop_id,entity_type,entity_id,actor_user_id,actor_name,action,details,event_type,metadata_json) VALUES(?,'marketplace',?,?,'','WB_MCP_STOCKS',?,'WB_MCP_STOCKS','{}')`, scope.WorkshopID, scope.ConnectionID, scope.UserID, result)
	if auditErr != nil {
		return ErrStorage
	}
	return err
}
