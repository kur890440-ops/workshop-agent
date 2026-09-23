package mcpclient

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"workshop-agent/internal/integrations/wbmcp"
	wb "workshop-agent/internal/marketplace/wildberries"
)

// A real child process running the same server package over actual OS stdio pipes.
func TestMCPChild(t *testing.T) {
	if os.Getenv("DAY16_TEST_CHILD") == "" {
		return
	}
	if os.Getenv("WB_API_TOKEN") != "" || os.Getenv("TELEGRAM_BOT_TOKEN") != "" {
		os.Exit(9)
	}
	if os.Getenv("DAY16_TEST_CHILD") == "hang" {
		time.Sleep(time.Minute)
		os.Exit(0)
	}
	if os.Getenv("DAY16_TEST_CHILD") == "bad" {
		fmt.Println("not MCP JSON")
		os.Exit(0)
	}
	server, e := wbmcp.New(wb.New(""))
	if e != nil {
		os.Exit(2)
	}
	if e = server.Run(context.Background(), &mcp.StdioTransport{}); e != nil {
		os.Exit(3)
	}
	os.Exit(0)
}
func child(t *testing.T, ctx context.Context, mode string) *exec.Cmd {
	t.Helper()
	exe, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	cmd := exec.CommandContext(ctx, exe, "-test.run=^TestMCPChild$")
	cmd.Env = append(ChildEnvironment(), "DAY16_TEST_CHILD="+mode)
	return cmd
}
func TestSTDIOInitializeListAndProcessCleanup(t *testing.T) {
	t.Setenv("WB_API_TOKEN", "synthetic-secret")
	t.Setenv("TELEGRAM_BOT_TOKEN", "synthetic-secret")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := child(t, ctx, "server")
	result, e := discoverCommand(ctx, cmd)
	if e != nil {
		t.Fatal(e)
	}
	if result.Server != "workshop-agent-wb" || result.Protocol != "2025-11-25" || !result.SessionClosed || !result.ChildExited || cmd.ProcessState == nil || !cmd.ProcessState.Success() {
		t.Fatal(result, cmd.ProcessState)
	}
	var names []string
	for _, tool := range result.Tools {
		names = append(names, tool.Name)
		if tool.Description == "" || !tool.ReadOnly {
			t.Fatal(tool)
		}
	}
	if !slices.Equal(names, []string{"wb_get_new_orders", "wb_get_order_statuses", "wb_get_products", "wb_get_seller", "wb_get_wb_stocks"}) {
		t.Fatal(names)
	}
	if strings.Contains(strings.Join(cmd.Env, "\n"), "synthetic-secret") {
		t.Fatal("credential inherited")
	}
}
func TestFailedInitializeAndTimeoutReapChild(t *testing.T) {
	for _, mode := range []string{"bad", "hang"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel()
			cmd := child(t, ctx, mode)
			_, e := discoverCommand(ctx, cmd)
			if e == nil {
				t.Fatal("expected connection error")
			}
			if cmd.ProcessState == nil {
				t.Fatal("child not waited")
			}
		})
	}
}
func TestReportEscapesDiscoveredContent(t *testing.T) {
	d := Discovery{Server: "fixture", Transport: "stdio", Tools: []Tool{{Name: "fixture", Description: "<script>alert(1)</script>", ReadOnly: true}}, SessionClosed: true, ChildExited: true}
	path, e := WriteReport(filepath.Join(t.TempDir(), "report"), d)
	if e != nil {
		t.Fatal(e)
	}
	raw, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(raw), "<script>") {
		t.Fatal("unescaped external content")
	}
	if !strings.Contains(string(raw), "&lt;script&gt;") {
		t.Fatal("description missing")
	}
}
