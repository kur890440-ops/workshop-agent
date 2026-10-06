//go:build windows

package config

import (
	"path/filepath"
	"testing"
)

func TestSpeechWindowsProjectRelativePaths(t *testing.T) {
	t.Setenv("SPEECH_MODEL_PATH", "assets/gigaam-v3-ctc")
	t.Setenv("SPEECH_RUNTIME_PATH", "assets/onnxruntime/onnxruntime.dll")
	c, err := loadSpeech()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	t.Chdir(root)
	for _, path := range []string{c.ModelPath, c.RuntimePath} {
		actual, err := filepath.Abs(path)
		if err != nil || actual != filepath.Join(root, path) {
			t.Fatal(actual, err)
		}
	}
	// A leading slash is drive-rooted on Windows, not application-relative.
	leading, err := filepath.Abs("/assets/gigaam-v3-ctc")
	if err != nil || leading == filepath.Join(root, "assets/gigaam-v3-ctc") {
		t.Fatal(leading, err)
	}
}
