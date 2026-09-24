package mcpclient

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"workshop-agent/internal/integrations/wbmcp"
	wb "workshop-agent/internal/marketplace/wildberries"
)

func TestInMemoryInitializeListAndCleanup(t *testing.T) {
	server, e := wbmcp.New(wb.New(""))
	if e != nil {
		t.Fatal(e)
	}
	service, e := NewInMemory(context.Background(), server)
	if e != nil {
		t.Fatal(e)
	}
	if e = service.Close(); e != nil {
		t.Fatal(e)
	}
	result := service.State()
	if result.Server != "workshop-agent-wb" || result.Protocol != "2025-11-25" || result.Transport != "in-memory" || !result.SessionClosed || !result.ServerClosed {
		t.Fatal(result)
	}
	var names []string
	for _, tool := range result.Tools {
		names = append(names, tool.Name)
		if !tool.ReadOnly {
			t.Fatal(tool)
		}
	}
	if !slices.Equal(names, []string{"wb_get_new_orders", "wb_get_order_statuses", "wb_get_prices", "wb_get_products", "wb_get_seller", "wb_get_wb_stocks"}) {
		t.Fatal(names)
	}
	if _, e = service.Stocks(context.Background(), "fixture"); e != Error("mcp_closed") {
		t.Fatal(e)
	}
}
func TestReportEscapesDiscoveredContent(t *testing.T) {
	d := Discovery{Server: "fixture", Transport: "in-memory", Tools: []Tool{{Name: "fixture", Description: "<script>alert(1)</script>", ReadOnly: true}}, SessionClosed: true, ServerClosed: true}
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
