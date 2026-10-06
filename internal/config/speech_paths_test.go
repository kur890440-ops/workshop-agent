package config

import (
	"os"
	"path/filepath"
	"testing"
	"workshop-agent/internal/speech"
)

func TestSpeechExecutableRootIndependentOfCWD(t *testing.T) {
	root, elsewhere := t.TempDir(), t.TempDir()
	for _, layout := range []string{"bin", "."} {
		t.Run(layout, func(t *testing.T) {
			if err := os.MkdirAll(filepath.Join(root, "assets"), 0755); err != nil {
				t.Fatal(err)
			}
			exe := filepath.Join(root, layout, "workshop-agent.exe")
			c := speech.DefaultConfig()
			paths := resolveSpeechPaths(&c, elsewhere, exe)
			if paths.AppRoot != root || c.ModelPath != filepath.Join(root, "assets", "gigaam-v3-ctc") || c.RuntimePath != filepath.Join(root, "assets", "onnxruntime", "onnxruntime.dll") {
				t.Fatal(paths, c)
			}
			explicit := filepath.Join(elsewhere, "custom-model")
			c.ModelPath = explicit
			resolveSpeechPaths(&c, root, exe)
			if c.ModelPath != explicit {
				t.Fatal(c.ModelPath)
			}
		})
	}
}
