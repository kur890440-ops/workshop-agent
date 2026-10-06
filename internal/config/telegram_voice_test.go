package config

import "testing"

func TestTelegramVoiceShowTranscript(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  bool
		bad   bool
	}{{"", true, false}, {"true", true, false}, {"false", false, false}, {"wrong", false, true}} {
		t.Run(tc.value, func(t *testing.T) {
			t.Setenv("TELEGRAM_VOICE_SHOW_TRANSCRIPT", tc.value)
			got, err := loadVoiceShowTranscript()
			if (err != nil) != tc.bad || !tc.bad && got != tc.want {
				t.Fatal(got, err)
			}
		})
	}
}
