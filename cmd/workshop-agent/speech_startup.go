package main

import (
	"log/slog"
	"os"
	"runtime/debug"
	"time"
	"workshop-agent/internal/config"
	"workshop-agent/internal/speech"
)

var processStartedAt = time.Now().UTC()

func logSpeechStartup(cfg *config.Config, status speech.SpeechProviderStatus) {
	revision, modified := "unknown", "unknown"
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				revision = setting.Value
			case "vcs.modified":
				modified = setting.Value
			}
		}
	}
	p := cfg.SpeechPaths
	slog.Info("speech startup", "pid", os.Getpid(), "process_started_at", processStartedAt.Format(time.RFC3339),
		"application_version", version, "build_commit", revision, "build_modified", modified,
		"cwd", p.CWD, "executable", p.Executable, "app_root", p.AppRoot,
		"speech_enabled", cfg.Speech.Enabled, "provider", cfg.Speech.Provider,
		"raw_model_path", p.RawModelPath, "resolved_model_path", p.ModelPath, "model_path_exists", p.ModelPathExists,
		"raw_runtime_path", p.RawRuntimePath, "resolved_runtime_path", p.RuntimePath, "runtime_path_exists", p.RuntimePathExists,
		"model_loaded", status.ModelLoaded, "runtime_loaded", status.RuntimeLoaded, "device", status.Device,
		"last_error", status.LastError, "load_error_detail", status.LoadErrorDetail, "fallback_reason", status.FallbackReason)
}
