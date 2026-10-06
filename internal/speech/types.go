// Package speech implements optional, local speech input without owning business state.
package speech

import (
	"context"
	"errors"
	"time"
)

type Code string

const (
	Success            Code = "SUCCESS"
	Disabled           Code = "DISABLED"
	ModelUnavailable   Code = "MODEL_NOT_AVAILABLE"
	InvalidAssets      Code = "INVALID_MODEL_ASSETS"
	RuntimeUnavailable Code = "RUNTIME_NOT_AVAILABLE"
	TooLarge           Code = "AUDIO_TOO_LARGE"
	TooLong            Code = "AUDIO_TOO_LONG"
	Unsupported        Code = "UNSUPPORTED_AUDIO"
	Empty              Code = "EMPTY_TRANSCRIPT"
	Failed             Code = "RECOGNITION_FAILED"
	Cancelled          Code = "CANCELLED"
	Timeout            Code = "TIMEOUT"
	Busy               Code = "BUSY"
)

func (c Code) Error() string { return string(c) }
func ErrorCode(err error) Code {
	if err == nil {
		return Success
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return Timeout
	}
	if errors.Is(err, context.Canceled) {
		return Cancelled
	}
	var c Code
	if errors.As(err, &c) {
		return c
	}
	return Failed
}

type Config struct {
	Enabled                                  bool
	Provider, ModelPath, RuntimePath, Device string
	CPUFallback                              bool
	MaxBytes                                 int64
	MaxDuration, Timeout                     time.Duration
	MaxConcurrentJobs                        int
}

func DefaultConfig() Config {
	return Config{Provider: "gigaam", ModelPath: "assets/gigaam-v3-ctc", RuntimePath: "assets/onnxruntime/onnxruntime.dll", Device: "auto", CPUFallback: true, MaxBytes: 20 << 20, MaxDuration: 120 * time.Second, Timeout: 90 * time.Second, MaxConcurrentJobs: 1}
}

type AudioInput struct {
	Data                       []byte
	MimeType, FileName, Source string
	SourceMessageID            int64
	Duration                   time.Duration
}
type Timings struct{ Decode, Preprocess, Inference, Tokens time.Duration }
type SpeechRecognitionResult struct {
	Text, Language, Provider, Model, Device string
	AudioDuration, ProcessingDuration       time.Duration
	Status, ErrorCode                       Code
	DecodeStatus                            Code
	Timings                                 Timings
}
type SpeechProviderStatus struct {
	Enabled                    bool
	Provider, Model, Device    string
	ModelLoaded, RuntimeLoaded bool
	LastError                  Code
	LoadErrorDetail            string `json:",omitempty"`
	FallbackReason             string `json:",omitempty"`
}
type SpeechRecognitionProvider interface {
	Name() string
	Transcribe(context.Context, AudioInput) (SpeechRecognitionResult, error)
	Status(context.Context) SpeechProviderStatus
}
type PCM struct {
	Samples    []float32
	SampleRate int
}
type AudioNormalizer interface {
	Normalize(context.Context, AudioInput) (PCM, error)
}
