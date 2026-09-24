package config

import (
	"github.com/joho/godotenv"
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

// WBForMCPFile is called only by the server, once at startup. Missing or invalid
// local configuration leaves discovery available and tool calls unconfigured.
// The parent passes a trusted local filename, never the credential value.
func WBForMCPFile(noToken bool, filename string) *wildberries.Client {
	if noToken || filename == "" {
		return WBForMCP(noToken)
	}
	values, err := godotenv.Read(filename)
	if err != nil {
		return wildberries.New("")
	}
	return wildberries.NewWithProfile(values["WB_API_TOKEN"], values["WB_API_PROFILE"])
}
