package mcpclient

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	wb "workshop-agent/internal/marketplace/wildberries"
)

const StocksTool = "wb_get_wb_stocks"

type NoArgs struct{}
type Envelope[T any] struct {
	Source        string `json:"source"`
	FetchedAt     string `json:"fetched_at"`
	Complete      bool   `json:"complete"`
	UntrustedData bool   `json:"untrusted_data"`
	Data          T      `json:"data"`
}

type Error string

func (e Error) Error() string { return string(e) }

// StocksResult contains only the structured, bounded tool result and safe trace.
type StocksResult struct {
	Value      Envelope[[]wb.Stock] `json:"value"`
	Discovery  Discovery            `json:"discovery"`
	DurationMS int64                `json:"duration_ms"`
}

// Service owns one long-lived SDK session. The child reads its own configuration;
// neither this service nor tool arguments receive credentials.
type Service struct {
	mu        sync.Mutex
	command   func(context.Context) *exec.Cmd
	session   *mcp.ClientSession
	transport *trackedTransport
	cmd       *exec.Cmd
	cancel    context.CancelFunc
	discovery Discovery
	seller    string
	verified  time.Time
	closed    bool
	cancelled bool
}

func New(executable, envFile, database string) *Service {
	return NewCommand(executable, "-env-file", envFile, "-database", database)
}

// NewCommand accepts administrator-owned launch configuration, never user input.
// It also permits the application's explicit, credential-free mock report mode.
func NewCommand(executable string, args ...string) *Service {
	args = append([]string(nil), args...)
	return &Service{command: func(ctx context.Context) *exec.Cmd {
		cmd := exec.CommandContext(ctx, executable, args...)
		cmd.Env = ChildEnvironment()
		cmd.Stderr = io.Discard
		return cmd
	}}
}

func (s *Service) connect(ctx context.Context) error {
	if s.closed {
		return Error("mcp_closed")
	}
	if s.session != nil {
		return nil
	}
	life, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.cancelled = false
	s.cmd = s.command(life)
	s.transport = &trackedTransport{inner: &mcp.CommandTransport{Command: s.cmd, TerminateDuration: time.Second}}
	var err error
	s.session, err = NewClient().Connect(ctx, s.transport, nil)
	if err != nil {
		s.cleanup()
		return Error("mcp_connection_error")
	}
	s.discovery, err = listTools(ctx, s.session)
	if err != nil {
		s.cleanup()
		return Error("mcp_discovery_error")
	}
	if s.discovery.Server != "workshop-agent-wb" || s.discovery.WriteToolsExposed != 0 {
		s.cleanup()
		return Error("mcp_tool_policy_error")
	}
	for _, name := range []string{StocksTool, "wb_get_seller"} {
		found := false
		for _, tool := range s.discovery.Tools {
			if tool.Name == name && tool.ReadOnly {
				found = true
			}
		}
		if !found {
			s.cleanup()
			return Error("mcp_tool_policy_error")
		}
	}
	return nil
}

// callTool is the only dispatch point. Discovery does not grant execution rights.
func (s *Service) callTool(ctx context.Context, name string, input NoArgs, out any) error {
	if name != StocksTool && name != "wb_get_seller" {
		return Error("mcp_tool_not_allowed")
	}
	result, err := s.session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: input})
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return Error("wb_timeout")
		}
		if ctx.Err() != nil {
			return Error("wb_cancelled")
		}
		return Error("mcp_internal_error")
	}
	if result.IsError {
		for _, content := range result.Content {
			if text, ok := content.(*mcp.TextContent); ok {
				code := strings.SplitN(text.Text, ":", 2)[0]
				switch code {
				case "wb_configuration_error", "wb_authentication_error", "wb_access_denied", "wb_rate_limit", "wb_timeout", "wb_cancelled", "wb_api_error", "wb_result_limit", "wb_output_limit", "invalid_tool_arguments":
					return Error(code)
				}
			}
		}
		return Error("mcp_internal_error")
	}
	raw, err := json.Marshal(result.StructuredContent)
	if err != nil || len(raw) > 1<<20 || string(raw) == "null" {
		return Error("mcp_invalid_result")
	}
	if json.Unmarshal(raw, out) != nil {
		return Error("mcp_invalid_result")
	}
	return nil
}

func (s *Service) Stocks(ctx context.Context, expectedSeller string) (out StocksResult, err error) {
	started := time.Now()
	defer func() { out.DurationMS = time.Since(started).Milliseconds() }()
	if expectedSeller == "" {
		return out, Error("wb_identity_required")
	}
	// Fail quickly on concurrent requests rather than queueing repeated WB reads.
	if !s.mu.TryLock() {
		return out, Error("mcp_busy")
	}
	defer s.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 100*time.Second)
	defer cancel()
	defer func() {
		if ctx.Err() != nil {
			s.cancelled = true
			_ = s.cleanup()
		}
	}()
	if err = s.connect(ctx); err != nil {
		return out, err
	}
	out.Discovery = s.discovery
	if s.seller != expectedSeller || time.Since(s.verified) >= 24*time.Hour {
		var seller Envelope[wb.Seller]
		if err = s.callTool(ctx, "wb_get_seller", NoArgs{}, &seller); err != nil {
			return out, err
		}
		if !seller.Complete || seller.Source != "wildberries" || seller.Data.ID != expectedSeller {
			return out, Error("wb_identity_mismatch")
		}
		s.seller, s.verified = expectedSeller, time.Now()
	}
	err = s.callTool(ctx, StocksTool, NoArgs{}, &out.Value)
	if err != nil {
		if err == Error("wb_authentication_error") || err == Error("wb_access_denied") {
			s.seller = ""
		}
		return out, err
	}
	if !out.Value.Complete || !out.Value.UntrustedData || out.Value.Source != "wildberries" || out.Value.Data == nil {
		return StocksResult{}, Error("mcp_invalid_result")
	}
	if _, e := time.Parse(time.RFC3339, out.Value.FetchedAt); e != nil {
		return StocksResult{}, Error("mcp_invalid_result")
	}
	for _, row := range out.Value.Data {
		if row.NmID <= 0 || row.ChrtID <= 0 || row.WarehouseID <= 0 || row.Quantity < 0 {
			return StocksResult{}, Error("mcp_invalid_result")
		}
	}
	return out, nil
}

func (s *Service) cleanup() error {
	var err error
	if s.session != nil {
		err = s.session.Close()
		s.discovery.SessionClosed = true
		s.session = nil
	}
	if s.transport != nil && s.transport.conn != nil {
		if e := s.transport.conn.Close(); e != nil {
			err = e
		}
	}
	if s.cancel != nil {
		s.cancel()
	}
	s.discovery.ChildExited = s.cmd != nil && s.cmd.ProcessState != nil && s.cmd.ProcessState.Exited()
	s.seller = ""
	// Cancellation may be reported by the SDK again while closing an already
	// cancelled call. A reaped child and closed session still mean cleanup worked.
	if s.discovery.ChildExited && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
		err = nil
	}
	var exitErr *exec.ExitError
	if s.cancelled && s.discovery.ChildExited && errors.As(err, &exitErr) {
		err = nil
	}
	if err != nil {
		return Error("mcp_cleanup_error")
	}
	return nil
}
func (s *Service) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return s.cleanup()
}
func (s *Service) State() Discovery { s.mu.Lock(); defer s.mu.Unlock(); return s.discovery }
