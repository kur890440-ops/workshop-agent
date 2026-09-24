package mcpclient

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"workshop-agent/internal/integrations/wbmcpfixture"
)

func TestDay17Child(t *testing.T) {
	if len(os.Args) < 3 || os.Args[len(os.Args)-2] != "--" {
		return
	}
	if os.Getenv("WB_API_TOKEN") != "" || os.Getenv("TELEGRAM_BOT_TOKEN") != "" {
		os.Exit(9)
	}
	if wbmcpfixture.Run(context.Background(), os.Args[len(os.Args)-1]) != nil {
		os.Exit(2)
	}
	os.Exit(0)
}
func stockClient(t *testing.T, mode string) *Service {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	s := NewCommand(exe, "-test.run=^TestDay17Child$", "--", mode)
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return s
}
func TestDay17STDIOStocksAndSafeFailures(t *testing.T) {
	t.Setenv("WB_API_TOKEN", "synthetic-secret-marker")
	t.Setenv("TELEGRAM_BOT_TOKEN", "synthetic-secret-marker")
	for _, tc := range []struct{ mode, code string }{
		{"success", ""}, {"missing", "wb_configuration_error"}, {"auth", "wb_authentication_error"},
		{"forbidden", "wb_access_denied"}, {"rate", "wb_rate_limit"}, {"timeout", "wb_timeout"},
		{"api", "wb_api_error"}, {"mismatch", "wb_identity_mismatch"},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			s := stockClient(t, tc.mode)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			out, err := s.Stocks(ctx, "day17-fixture")
			if tc.code == "" {
				if err != nil || len(out.Value.Data) != 3 || out.Value.Data[0].Quantity != 12 || out.Value.Data[2].Quantity != 0 {
					t.Fatalf("result=%+v error=%v", out, err)
				}
				// A second call reuses the actual session and identity.
				pid := s.cmd.Process.Pid
				if _, e := s.Stocks(ctx, "day17-fixture"); e != nil || s.cmd.Process.Pid != pid {
					t.Fatal("session not reused", e)
				}
			} else if err != Error(tc.code) {
				t.Fatalf("want %s got %v", tc.code, err)
			}
			if out.Discovery.Server != "workshop-agent-wb" || len(out.Discovery.Tools) != 5 || out.Discovery.WriteToolsExposed != 0 {
				t.Fatal(out.Discovery)
			}
			if e := s.callTool(ctx, "write_anything", NoArgs{}, nil); e != Error("mcp_tool_not_allowed") {
				t.Fatal(e)
			}
			raw, _ := json.Marshal(out)
			if strings.Contains(string(raw), "synthetic-secret-marker") || err != nil && strings.Contains(err.Error(), "synthetic-secret-marker") {
				t.Fatal("secret leaked")
			}
			if e := s.Close(); e != nil {
				t.Fatal(e)
			}
			if state := s.State(); !state.SessionClosed || !state.ChildExited {
				t.Fatal(state)
			}
		})
	}
}
func TestDay17NoServerIsConnectionError(t *testing.T) {
	s := NewCommand("nonexistent-day17-server.exe")
	defer s.Close()
	if _, err := s.Stocks(context.Background(), "day17-fixture"); err != Error("mcp_connection_error") {
		t.Fatal(err)
	}
}
func TestDay17CancellationAndCleanup(t *testing.T) {
	s := stockClient(t, "wait")
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, err := s.Stocks(ctx, "day17-fixture")
	if err != Error("wb_timeout") {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatalf("%v: transport=%T %v; process=%v", err, s.transport.conn.err, s.transport.conn.err, s.cmd.ProcessState)
	}
	if !s.State().ChildExited {
		t.Fatal("child not reaped")
	}
}
