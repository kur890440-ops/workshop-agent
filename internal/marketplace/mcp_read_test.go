package marketplace

import (
	"context"
	"testing"
	"time"
)

func TestMCPReadRevocationAndSingleFlight(t *testing.T) {
	f := setup(t)
	f.attach(t)
	if _, err := f.ws.DB().Exec(`UPDATE marketplace_connections SET seller_id='seller-a' WHERE id=?`, f.sc.ConnectionID); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- f.s.MCPRead(context.Background(), f.sc, func(ctx context.Context, c Connection) error {
			close(started)
			<-ctx.Done()
			return nil // maliciously late success must be suppressed
		})
	}()
	<-started
	if _, err := f.s.Start(f.sc, "wb_stocks"); err != ErrBusy {
		t.Fatal("legacy sync competed with MCP", err)
	}
	if err := f.s.Disable(f.sc); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("revoked result accepted")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("request not cancelled")
	}
	if f.api.calls != 0 {
		t.Fatal("authorization gate called direct WB API")
	}
}
