package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"os"
	"os/signal"
	"workshop-agent/internal/config"
	"workshop-agent/internal/integrations/wbmcp"
)

func main() {
	noToken := flag.Bool("no-token", false, "Disable WB credentials; initialize and tools/list remain available")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "unexpected server arguments")
		os.Exit(2)
	}
	server, err := wbmcp.New(config.WBForMCP(*noToken))
	if err != nil {
		fmt.Fprintln(os.Stderr, "WB MCP configuration error")
		os.Exit(1)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	if err = server.Run(ctx, &mcp.StdioTransport{}); err != nil && ctx.Err() == nil {
		fmt.Fprintln(os.Stderr, "WB MCP session closed with error")
		os.Exit(1)
	}
}
