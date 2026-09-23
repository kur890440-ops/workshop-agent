package config

import "testing"

func TestMCPNoTokenOverridesEnvironment(t *testing.T) {
	t.Setenv("WB_API_TOKEN", "synthetic-test-secret")
	if WBForMCP(true).Configured() {
		t.Fatal("no-token inherited a credential")
	}
	if !WBForMCP(false).Configured() {
		t.Fatal("server env config not used")
	}
	t.Setenv("WB_API_TOKEN", "")
	if WBForMCP(false).Configured() {
		t.Fatal("unexpected credential")
	}
}
