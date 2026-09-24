package config

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestMCPServerOwnsFixtureConfig(t *testing.T) {
	t.Setenv("WB_API_TOKEN", "parent-token-not-to-inherit")
	path := filepath.Join(t.TempDir(), "fixture.env")
	if err := os.WriteFile(path, []byte("WB_API_TOKEN=synthetic-server-only\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if !WBForMCPFile(false, path).Configured() {
		t.Fatal("fixture not configured")
	}
	if WBForMCPFile(true, path).Configured() {
		t.Fatal("no-token override ignored")
	}
	missing := WBForMCPFile(false, path+".missing")
	if missing.Configured() {
		t.Fatal("unexpected parent credential fallback")
	}
	if _, err := missing.WBStocks(context.Background()); err == nil {
		t.Fatal("missing config not detected")
	}
}
