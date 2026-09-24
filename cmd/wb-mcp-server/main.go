package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	_ "github.com/glebarez/sqlite"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"os"
	"os/signal"
	"workshop-agent/internal/config"
	"workshop-agent/internal/integrations/wbmcp"
	"workshop-agent/internal/marketplace"
)

func main() {
	noToken := flag.Bool("no-token", false, "Disable WB credentials; initialize and tools/list remain available")
	envFile := flag.String("env-file", "", "Server-owned local configuration file; read once at startup")
	database := flag.String("database", "", "Existing application DB for shared WB rate deadlines; no migrations")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "unexpected server arguments")
		os.Exit(2)
	}
	api := config.WBForMCPFile(*noToken, *envFile)
	if *database != "" && !*noToken {
		if info, err := os.Stat(*database); err != nil || info.IsDir() {
			fmt.Fprintln(os.Stderr, "WB MCP cooldown configuration error")
			os.Exit(1)
		}
		db, err := sql.Open("sqlite", *database)
		if err != nil {
			fmt.Fprintln(os.Stderr, "WB MCP cooldown configuration error")
			os.Exit(1)
		}
		defer db.Close()
		db.SetMaxOpenConns(1)
		if _, err = db.Exec("PRAGMA busy_timeout=5000"); err != nil {
			fmt.Fprintln(os.Stderr, "WB MCP cooldown configuration error")
			os.Exit(1)
		}
		api.SetCooldownStore(marketplace.MCPCooldowns(db))
	}
	server, err := wbmcp.New(api)
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
