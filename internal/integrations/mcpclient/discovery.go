// Package mcpclient is the Workshop Agent's local SDK-backed MCP boundary.
// Explicit read-only calls never accept credentials or LLM-selected tools.
package mcpclient

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"sort"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type Tool struct {
	Name         string          `json:"name"`
	Description  string          `json:"description"`
	ReadOnly     bool            `json:"read_only"`
	OutputSchema json.RawMessage `json:"output_schema"`
	InputSchema  json.RawMessage `json:"input_schema"`
}
type Discovery struct {
	Server             string `json:"server"`
	Protocol           string `json:"protocol"`
	Transport          string `json:"transport"`
	Tools              []Tool `json:"tools"`
	LocalMutationTools int    `json:"local_mutation_tools"`
	WriteToolsExposed  int    `json:"write_tools_exposed"`
	SessionClosed      bool   `json:"session_closed"`
	ServerClosed       bool   `json:"server_closed"`
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
func listTools(ctx context.Context, session *mcp.ClientSession) (out Discovery, err error) {
	out.Transport = "in-memory"
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
			outputSchema, e := json.Marshal(tool.OutputSchema)
			if e != nil {
				return out, errors.New("mcp_invalid_schema")
			}
			readOnly := tool.Annotations != nil && tool.Annotations.ReadOnlyHint
			if !readOnly && tool.Name == "schedule_wb_daily_sync" {
				out.LocalMutationTools++
			} else if !readOnly {
				out.WriteToolsExposed++
			}
			out.Tools = append(out.Tools, Tool{Name: tool.Name, Description: tool.Description, ReadOnly: readOnly, InputSchema: schema, OutputSchema: outputSchema})
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
