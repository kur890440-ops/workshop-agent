package mcpmanager

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestProductionArchitectureNoChildProcessListenerOrSecondScheduler(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	paths := []string{"internal/integrations/mcpmanager", "internal/integrations/mcpclient", "internal/background", "cmd/workshop-agent"}
	for _, dir := range paths {
		entries, e := os.ReadDir(filepath.Join(root, dir))
		if e != nil {
			t.Fatal(e)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
				continue
			}
			path := filepath.Join(root, dir, entry.Name())
			file, e := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if e != nil {
				t.Fatal(e)
			}
			for _, imp := range file.Imports {
				value, _ := strconv.Unquote(imp.Path.Value)
				// Day30 explicitly authorizes one Web HTTP listener in the composition root.
				// MCP transports and background executors remain in-process only.
				webComposition := dir == "cmd/workshop-agent" && entry.Name() == "main.go"
				if value == "os/exec" || (!webComposition && (value == "net" || value == "net/http")) {
					t.Fatalf("process/listener/HTTP bypass: %s: %s", path, value)
				}
			}
			ast.Inspect(file, func(node ast.Node) bool {
				if dir == "internal/integrations/mcpmanager" {
					if _, ok := node.(*ast.GoStmt); ok {
						t.Errorf("MCP handler owns goroutine: %s", path)
					}
				}
				if call, ok := node.(*ast.CallExpr); ok {
					if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
						if sel.Sel.Name == "NewTicker" && dir == "internal/background" && entry.Name() != "scheduler.go" {
							t.Errorf("second scheduler: %s", path)
						}
						if dir == "internal/integrations/mcpmanager" && (sel.Sel.Name == "NewTicker" || sel.Sel.Name == "Sleep") {
							t.Errorf("scheduling in MCP adapter: %s", path)
						}
					}
				}
				return true
			})
		}
	}
}
