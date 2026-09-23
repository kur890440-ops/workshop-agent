// Package mcpclient is the Workshop Agent's local MCP discovery boundary.
// It deliberately exposes no automatic tool invocation or credential API.
package mcpclient

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"sort"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	ReadOnly    bool            `json:"read_only"`
	InputSchema json.RawMessage `json:"input_schema"`
}
type Discovery struct {
	Server            string `json:"server"`
	Protocol          string `json:"protocol"`
	Transport         string `json:"transport"`
	Tools             []Tool `json:"tools"`
	WriteToolsExposed int    `json:"write_tools_exposed"`
	SessionClosed     bool   `json:"session_closed"`
	ChildExited       bool   `json:"child_exited"`
}

// ChildEnvironment reads only OS runtime essentials, never WB/LLM/Telegram credentials.
func ChildEnvironment() []string {
	out := []string{}
	for _, key := range []string{"SystemRoot", "WINDIR", "TEMP", "TMP"} {
		if value, ok := os.LookupEnv(key); ok {
			out = append(out, key+"="+value)
		}
	}
	return out
}

// NewClient selects the SDK's initialize lifecycle for the Day 16 discovery flow.
// Skip the newer stateless probe locally; initialize and tools/list still travel
// over the real transport and are fully implemented by the official SDK.
func NewClient() *mcp.Client {
	client := mcp.NewClient(&mcp.Implementation{Name: "workshop-agent-mcp-smoke", Version: "day16.1"}, &mcp.ClientOptions{Capabilities: &mcp.ClientCapabilities{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	client.AddSendingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method == "server/discover" {
				return nil, &jsonrpc.Error{Code: -32601, Message: "initialize lifecycle selected"}
			}
			return next(ctx, method, req)
		}
	})
	return client
}
func Discover(ctx context.Context, executable string) (Discovery, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "-no-token")
	cmd.Env = ChildEnvironment()
	cmd.Stderr = io.Discard
	return discoverCommand(ctx, cmd)
}

// Track Close once, including failed initialization. The SDK transport owns Wait.
type trackedTransport struct {
	inner *mcp.CommandTransport
	conn  *trackedConnection
}
type trackedConnection struct {
	mcp.Connection
	once sync.Once
	err  error
}

func (c *trackedConnection) Close() error {
	c.once.Do(func() { c.err = c.Connection.Close() })
	return c.err
}
func (t *trackedTransport) Connect(ctx context.Context) (mcp.Connection, error) {
	c, err := t.inner.Connect(ctx)
	if err != nil {
		return nil, err
	}
	t.conn = &trackedConnection{Connection: c}
	return t.conn, nil
}
func discoverCommand(ctx context.Context, cmd *exec.Cmd) (out Discovery, err error) {
	out.Transport = "stdio"
	transport := &trackedTransport{inner: &mcp.CommandTransport{Command: cmd, TerminateDuration: time.Second}}
	client := NewClient()
	session, e := client.Connect(ctx, transport, nil)
	defer func() {
		if session != nil {
			if e := session.Close(); e != nil && err == nil {
				err = errors.New("mcp_cleanup_error")
			}
			out.SessionClosed = true
		}
		if transport.conn != nil {
			if e := transport.conn.Close(); e != nil && err == nil {
				err = errors.New("mcp_cleanup_error")
			}
		}
		out.ChildExited = cmd.ProcessState != nil && cmd.ProcessState.Exited()
		if cmd.Process != nil && !out.ChildExited && err == nil {
			err = errors.New("mcp_child_cleanup_error")
		}
	}()
	if e != nil {
		return out, errors.New("mcp_connection_error")
	}
	initialized := session.InitializeResult()
	if initialized == nil || initialized.ServerInfo == nil {
		return out, errors.New("mcp_initialization_error")
	}
	out.Server = initialized.ServerInfo.Name
	out.Protocol = initialized.ProtocolVersion
	cursor := ""
	seenCursors := map[string]bool{}
	seenNames := map[string]bool{}
	for page := 0; page < 10; page++ {
		result, e := session.ListTools(ctx, &mcp.ListToolsParams{Cursor: cursor})
		if e != nil {
			return out, errors.New("mcp_discovery_error")
		}
		for _, tool := range result.Tools {
			if tool.Name == "" || tool.Description == "" || seenNames[tool.Name] || len(out.Tools) >= 50 {
				return out, errors.New("mcp_invalid_tool_list")
			}
			seenNames[tool.Name] = true
			schema, e := json.Marshal(tool.InputSchema)
			if e != nil {
				return out, errors.New("mcp_invalid_schema")
			}
			readOnly := tool.Annotations != nil && tool.Annotations.ReadOnlyHint
			if !readOnly {
				out.WriteToolsExposed++
			}
			out.Tools = append(out.Tools, Tool{Name: tool.Name, Description: tool.Description, ReadOnly: readOnly, InputSchema: schema})
		}
		if result.NextCursor == "" {
			sort.Slice(out.Tools, func(i, j int) bool { return out.Tools[i].Name < out.Tools[j].Name })
			return out, nil
		}
		cursor = result.NextCursor
		if seenCursors[cursor] {
			return out, errors.New("mcp_pagination_error")
		}
		seenCursors[cursor] = true
	}
	return out, errors.New("mcp_pagination_limit")
}
