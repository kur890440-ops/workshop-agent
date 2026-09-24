package mcpclient

import (
	"context"
	"encoding/json"
	"errors"
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

// Service owns the application's one in-process MCP client/server session pair.
// No credentials, operating-system processes or network listeners live here.
type Service struct {
	mu            sync.Mutex
	session       *mcp.ClientSession
	serverSession *mcp.ServerSession
	discovery     Discovery
	seller        string
	verified      time.Time
	closed        bool
}

// NewInMemory performs actual initialize and ListTools via the official SDK.
func NewInMemory(ctx context.Context, server *mcp.Server) (*Service, error) {
	if server == nil {
		return nil, Error("mcp_connection_error")
	}
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		return nil, Error("mcp_connection_error")
	}
	cs, err := NewClient().Connect(ctx, clientTransport, nil)
	if err != nil {
		_ = ss.Close()
		return nil, Error("mcp_connection_error")
	}
	s := &Service{session: cs, serverSession: ss}
	s.discovery, err = listTools(ctx, cs)
	if err != nil {
		_ = s.Close()
		return nil, Error("mcp_discovery_error")
	}
	if s.discovery.Server != "workshop-agent-wb" {
		_ = s.Close()
		return nil, Error("mcp_tool_policy_error")
	}
	for _, tool := range s.discovery.Tools {
		if !tool.ReadOnly && tool.Name != "schedule_wb_daily_sync" {
			_ = s.Close()
			return nil, Error("mcp_tool_policy_error")
		}
	}
	for _, name := range []string{StocksTool, "wb_get_seller", PricesTool} {
		if !s.readOnly(name) {
			_ = s.Close()
			return nil, Error("mcp_tool_policy_error")
		}
	}
	return s, nil
}
func (s *Service) readOnly(name string) bool {
	for _, tool := range s.discovery.Tools {
		if tool.Name == name {
			return tool.ReadOnly
		}
	}
	return false
}
func (s *Service) connect(ctx context.Context) error {
	if s.closed {
		return Error("mcp_closed")
	}
	if s.session == nil {
		return Error("mcp_connection_error")
	}
	return ctx.Err()
}

// Schedule is a dedicated local mutation path. The manager issues its one-use grant;
// neither this method nor the read-only dispatcher grants user/workshop authority.
func (s *Service) Schedule(ctx context.Context, grant string, input, out any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.connect(ctx); err != nil {
		return err
	}
	result, err := s.session.CallTool(ctx, &mcp.CallToolParams{Name: "schedule_wb_daily_sync", Arguments: input, Meta: mcp.Meta{"workshop-grant": grant}})
	if err != nil || result.IsError {
		return Error("schedule_denied_or_invalid")
	}
	raw, err := json.Marshal(result.StructuredContent)
	if err != nil || len(raw) > 65536 || json.Unmarshal(raw, out) != nil {
		return Error("mcp_invalid_result")
	}
	return nil
}

// callTool is the only dispatch point. Discovery does not grant execution rights.
func (s *Service) callTool(ctx context.Context, name string, input NoArgs, out any) error {
	if (name != StocksTool && name != "wb_get_seller" && name != "wb_get_prices") || !s.readOnly(name) {
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
		s.session = nil
		s.discovery.SessionClosed = true
	}
	if s.serverSession != nil {
		e := s.serverSession.Close()
		if err == nil {
			err = e
		}
		s.serverSession = nil
		s.discovery.ServerClosed = true
	}
	s.seller = ""
	if err != nil && !errors.Is(err, context.Canceled) {
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
func (s *Service) State() Discovery {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.discovery
	d.Tools = append([]Tool(nil), s.discovery.Tools...)
	for i := range d.Tools {
		d.Tools[i].InputSchema = append(json.RawMessage(nil), d.Tools[i].InputSchema...)
		d.Tools[i].OutputSchema = append(json.RawMessage(nil), d.Tools[i].OutputSchema...)
	}
	return d
}
