package config

import (
	"fmt"
	"os"
	"strconv"
)

func loadVoiceShowTranscript() (bool, error) {
	value := os.Getenv("TELEGRAM_VOICE_SHOW_TRANSCRIPT")
	if value == "" {
		return true, nil
	}
	enabled, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("invalid TELEGRAM_VOICE_SHOW_TRANSCRIPT")
	}
	return enabled, nil
}
