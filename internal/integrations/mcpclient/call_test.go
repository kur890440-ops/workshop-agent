package mcpclient

import (
	"context"
	"encoding/json"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"strings"
	"testing"
	"time"

	"workshop-agent/internal/integrations/wbmcpfixture"
)

func stockClient(t *testing.T, mode string) *Service {
	t.Helper()
	server, e := wbmcpfixture.Server(mode)
	if e != nil {
		t.Fatal(e)
	}
	s, e := NewInMemory(context.Background(), server)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if e := s.Close(); e != nil {
			t.Error(e)
		}
	})
	return s
}
func TestDay17InMemoryStocksAndSafeFailures(t *testing.T) {
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
				session := s.session
				if _, e := s.Stocks(ctx, "day17-fixture"); e != nil || s.session != session {
					t.Fatal("session not reused", e)
				}
			} else if err != Error(tc.code) {
				t.Fatalf("want %s got %v", tc.code, err)
			}
			if out.Discovery.Server != "workshop-agent-wb" || len(out.Discovery.Tools) != 6 || out.Discovery.WriteToolsExposed != 0 {
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
			if state := s.State(); !state.SessionClosed || !state.ServerClosed {
				t.Fatal(state)
			}
		})
	}
}
func TestDay17NoServerIsConnectionError(t *testing.T) {
	if _, e := NewInMemory(context.Background(), nil); e != Error("mcp_connection_error") {
		t.Fatal(e)
	}
}

func TestDay18PricesOverExistingInMemory(t *testing.T) {
	for _, mode := range []string{"success", "price_error", "missing"} {
		t.Run(mode, func(t *testing.T) {
			s := stockClient(t, mode)
			out, e := s.Prices(context.Background(), "day17-fixture")
			if mode == "success" {
				if e != nil || len(out.Data) != 1 || out.Data[0].PriceCents != 10000 {
					t.Fatal(out, e)
				}
			} else if e == nil {
				t.Fatal("expected controlled error")
			}
		})
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
	if s.State().SessionClosed {
		t.Fatal("request cancellation closed global session")
	}
	if _, e := s.Prices(context.Background(), "day17-fixture"); e != nil {
		t.Fatal("session unusable after cancellation", e)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	if !s.State().ServerClosed {
		t.Fatal("server not closed")
	}
}

func TestWriteClassificationBlocksWBExecution(t *testing.T) {
	server, e := wbmcpfixture.Server("success")
	if e != nil {
		t.Fatal(e)
	}
	// Override the otherwise trusted name with a mutation classification.
	mcp.AddTool(server, &mcp.Tool{Name: StocksTool, Description: "unsafe write", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false}}, func(context.Context, *mcp.CallToolRequest, NoArgs) (*mcp.CallToolResult, NoArgs, error) {
		t.Fatal("write handler invoked")
		return nil, NoArgs{}, nil
	})
	if _, e = NewInMemory(context.Background(), server); e != Error("mcp_tool_policy_error") {
		t.Fatal(e)
	}
}
