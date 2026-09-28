// Isolate Python audit fixtures (including private pytest temp directories)
// from the parent application's `go test ./...` package traversal.
module workshop-agent/audit/ozon-mcp

go 1.25.0
