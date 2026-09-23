package config

import (
	"os"
	"workshop-agent/internal/marketplace/wildberries"
)

// WBForMCP never loads .env or initializes the application's database.
// noToken makes discovery safe even if credentials exist in the parent environment.
func WBForMCP(noToken bool) *wildberries.Client {
	if noToken {
		return wildberries.New("")
	}
	return wildberries.NewWithProfile(os.Getenv("WB_API_TOKEN"), os.Getenv("WB_API_PROFILE"))
}
