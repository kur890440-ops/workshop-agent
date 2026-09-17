// Package bootstrap retains the CLI initialization entry point without creating demo data.
package bootstrap

import "workshop-agent/internal/storage"

func InitProject(databasePath string) error { return storage.InitDatabase(databasePath) }
