package config

import (
	"testing"
	"time"
)

func TestSpeechConfig(t *testing.T) {
	for _, k := range []string{"SPEECH_ENABLED", "SPEECH_PROVIDER", "SPEECH_DEVICE", "SPEECH_CPU_FALLBACK", "SPEECH_MAX_CONCURRENT_JOBS", "SPEECH_TIMEOUT", "SPEECH_MAX_DURATION", "SPEECH_MAX_FILE_SIZE_MB", "SPEECH_MODEL_PATH", "SPEECH_RUNTIME_PATH"} {
		t.Setenv(k, "")
	}
	c, e := loadSpeech()
	if e != nil || c.Enabled || c.MaxConcurrentJobs != 1 || c.Timeout != 90*time.Second {
		t.Fatal(c, e)
	}
	t.Setenv("SPEECH_ENABLED", "true")
	t.Setenv("SPEECH_MODEL_PATH", "assets/модель")
	c, e = loadSpeech()
	if e != nil || !c.Enabled || c.ModelPath != "assets/модель" {
		t.Fatal(c, e)
	}
	for k, v := range map[string]string{"SPEECH_DEVICE": "gpu", "SPEECH_PROVIDER": "remote", "SPEECH_TIMEOUT": "0s", "SPEECH_MAX_DURATION": "999h", "SPEECH_MAX_FILE_SIZE_MB": "-1", "SPEECH_MAX_CONCURRENT_JOBS": "0", "SPEECH_CPU_FALLBACK": "maybe"} {
		t.Run(k, func(t *testing.T) {
			t.Setenv(k, v)
			if _, e := loadSpeech(); e == nil {
				t.Fatal("invalid config accepted")
			}
		})
	}
}
