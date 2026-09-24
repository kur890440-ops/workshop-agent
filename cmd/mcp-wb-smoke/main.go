package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"
	"workshop-agent/internal/integrations/mcpclient"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	name := "wb-mcp-server-day16"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	server := flag.String("server", filepath.Join("bin", name), "Path to the local WB MCP server executable")
	reportRoot := flag.String("report-root", filepath.Join("reports", "day16-mcp"), "Directory for smoke report")
	flag.Parse()
	if flag.NArg() != 0 {
		return fmt.Errorf("unexpected smoke arguments")
	}
	executable, err := filepath.Abs(*server)
	if err != nil {
		return fmt.Errorf("invalid MCP executable path")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result, err := mcpclient.Discover(ctx, executable)
	if err != nil {
		if err.Error() == "mcp_connection_error" {
			return fmt.Errorf("MCP connection failed: check the server executable (-server); build it with go build -o bin/%s ./cmd/wb-mcp-server", name)
		}
		return fmt.Errorf("MCP smoke failed during discovery or cleanup (%s)", err)
	}
	if result.Server != "workshop-agent-wb" || len(result.Tools) == 0 || result.WriteToolsExposed != 0 || !result.SessionClosed || !result.ChildExited {
		return fmt.Errorf("MCP acceptance failed")
	}
	if err = mcpclient.Print(os.Stdout, result); err != nil {
		return fmt.Errorf("cannot print discovery result")
	}
	dir := filepath.Join(*reportRoot, time.Now().UTC().Format("20060102T150405.000000000Z"))
	path, err := mcpclient.WriteReport(dir, result)
	if err != nil {
		return fmt.Errorf("cannot write Day 16 report")
	}
	fmt.Println("Report:", path)
	return nil
}
