package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
	"workshop-agent/internal/speech"
)

func loadSpeech() (speech.Config, error) {
	c := speech.DefaultConfig()
	for key, p := range map[string]*string{"SPEECH_PROVIDER": &c.Provider, "SPEECH_MODEL_PATH": &c.ModelPath, "SPEECH_RUNTIME_PATH": &c.RuntimePath, "SPEECH_DEVICE": &c.Device} {
		if v := os.Getenv(key); v != "" {
			*p = v
		}
	}
	for key, p := range map[string]*bool{"SPEECH_ENABLED": &c.Enabled, "SPEECH_CPU_FALLBACK": &c.CPUFallback} {
		if v := os.Getenv(key); v != "" {
			b, e := strconv.ParseBool(v)
			if e != nil {
				return c, fmt.Errorf("invalid %s", key)
			}
			*p = b
		}
	}
	for key, p := range map[string]*time.Duration{"SPEECH_TIMEOUT": &c.Timeout, "SPEECH_MAX_DURATION": &c.MaxDuration} {
		if v := os.Getenv(key); v != "" {
			d, e := time.ParseDuration(v)
			if e != nil || d <= 0 || d > 10*time.Minute {
				return c, fmt.Errorf("invalid %s", key)
			}
			*p = d
		}
	}
	if v := os.Getenv("SPEECH_MAX_FILE_SIZE_MB"); v != "" {
		n, e := strconv.ParseInt(v, 10, 64)
		if e != nil || n < 1 || n > 100 {
			return c, fmt.Errorf("invalid SPEECH_MAX_FILE_SIZE_MB")
		}
		c.MaxBytes = n << 20
	}
	if v := os.Getenv("SPEECH_MAX_CONCURRENT_JOBS"); v != "" {
		n, e := strconv.Atoi(v)
		if e != nil || n < 1 || n > 8 {
			return c, fmt.Errorf("invalid SPEECH_MAX_CONCURRENT_JOBS")
		}
		c.MaxConcurrentJobs = n
	}
	if c.Provider != "gigaam" || (c.Device != "auto" && c.Device != "cuda" && c.Device != "cpu") {
		return c, fmt.Errorf("invalid speech provider/device")
	}
	return c, nil
}
