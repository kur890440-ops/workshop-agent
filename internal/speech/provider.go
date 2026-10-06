package speech

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Engine interface {
	Infer(context.Context, []float32, int) ([]float32, int, int, int, error)
	Close() error
}
type EngineFactory func(Config, Assets, string) (Engine, error)
type GigaAMProvider struct {
	cfg        Config
	normalizer AudioNormalizer
	assets     Assets
	engine     Engine
	status     SpeechProviderStatus
	mu         sync.RWMutex
	slots      chan struct{}
	// Binding sessions are not concurrency-safe. Reuse one serialized session;
	// admission may allow bounded parallel download/normalization.
	inference chan struct{}
}

var requestID atomic.Uint64

func New(cfg Config) *GigaAMProvider {
	return newProvider(cfg, Normalizer{cfg.MaxBytes, cfg.MaxDuration}, newONNXEngine)
}
func newProvider(cfg Config, n AudioNormalizer, f EngineFactory) *GigaAMProvider {
	if cfg.MaxConcurrentJobs < 1 {
		cfg.MaxConcurrentJobs = 1
	}
	p := &GigaAMProvider{cfg: cfg, normalizer: n, slots: make(chan struct{}, cfg.MaxConcurrentJobs), inference: make(chan struct{}, 1), status: SpeechProviderStatus{Enabled: cfg.Enabled, Provider: "gigaam", Model: "v3_ctc", Device: cfg.Device}}
	if !cfg.Enabled {
		p.status.LastError = Disabled
		return p
	}
	a, e := readAssets(cfg.ModelPath)
	if e != nil {
		p.status.LastError = ErrorCode(e)
		return p
	}
	p.assets = a
	devices := []string{cfg.Device}
	if cfg.Device == "auto" {
		devices = []string{"cuda"}
	}
	if devices[0] != "cpu" && cfg.CPUFallback {
		devices = append(devices, "cpu")
	}
	for _, device := range devices {
		p.engine, e = f(cfg, a, device)
		if e == nil {
			p.status.Device = device
			p.status.ModelLoaded = true
			p.status.RuntimeLoaded = true
			p.status.LastError = ""
			slog.Info("speech loaded", "provider", p.Name(), "model", a.Model, "device", device)
			return p
		}
		if device != "cpu" && cfg.CPUFallback {
			p.status.FallbackReason = fmt.Sprintf("%s: %v", device, e)
		}
	}
	p.status.LastError = ErrorCode(e)
	p.status.LoadErrorDetail = e.Error()
	return p
}
func (p *GigaAMProvider) Name() string { return "gigaam" }
func (p *GigaAMProvider) Status(context.Context) SpeechProviderStatus {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.status
}
func (p *GigaAMProvider) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.status.ModelLoaded = false
	p.status.RuntimeLoaded = false
	p.status.LastError = Disabled
	if p.engine != nil {
		e := p.engine.Close()
		p.engine = nil
		return e
	}
	return nil
}
func (p *GigaAMProvider) Transcribe(parent context.Context, a AudioInput) (r SpeechRecognitionResult, err error) {
	started := time.Now()
	id := requestID.Add(1)
	ctx, cancel := context.WithTimeout(parent, p.cfg.Timeout)
	defer cancel()
	s := p.Status(ctx)
	r = SpeechRecognitionResult{Language: "ru", Provider: p.Name(), Model: "v3_ctc", Device: s.Device, DecodeStatus: "NOT_RUN"}
	defer func() {
		r.ProcessingDuration = time.Since(started)
		r.Status = ErrorCode(err)
		if err != nil {
			r.ErrorCode = r.Status
		}
		p.mu.Lock()
		if p.status.ModelLoaded {
			p.status.LastError = r.ErrorCode
		}
		p.mu.Unlock()
		slog.Info("speech request", "speech_request_id", id, "provider", r.Provider, "model", r.Model, "device", r.Device, "source", a.Source, "audio_mime", a.MimeType, "audio_duration", r.AudioDuration, "audio_bytes", len(a.Data), "processing_duration", r.ProcessingDuration, "status", r.Status, "error_code", r.ErrorCode)
	}()
	if ctx.Err() != nil {
		return r, ctx.Err()
	}
	if !s.Enabled {
		return r, Disabled
	}
	if !s.ModelLoaded {
		return r, s.LastError
	}
	select {
	case p.slots <- struct{}{}:
		defer func() { <-p.slots }()
	default:
		return r, Busy
	}
	// Hold a read lease through native inference so Close cannot free active tensors/session.
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.engine == nil {
		return r, Disabled
	}
	t := time.Now()
	pcm, e := p.normalizer.Normalize(ctx, a)
	r.Timings.Decode = time.Since(t)
	r.DecodeStatus = ErrorCode(e)
	if e != nil {
		return r, e
	}
	r.AudioDuration = time.Duration(float64(len(pcm.Samples)) / float64(pcm.SampleRate) * float64(time.Second))
	t = time.Now()
	features, frames, e := Features(ctx, pcm.Samples, p.assets.Frontend)
	r.Timings.Preprocess = time.Since(t)
	if e != nil {
		return r, e
	}
	select {
	case p.inference <- struct{}{}:
		defer func() { <-p.inference }()
	case <-ctx.Done():
		return r, ctx.Err()
	}
	t = time.Now()
	logits, steps, classes, length, e := p.engine.Infer(ctx, features, frames)
	r.Timings.Inference = time.Since(t)
	if ctx.Err() != nil {
		return r, ctx.Err()
	}
	if e != nil {
		return r, e
	}
	t = time.Now()
	r.Text, e = DecodeCTC(logits, steps, classes, length, p.assets.BlankID, p.assets.Vocab)
	r.Timings.Tokens = time.Since(t)
	if e != nil {
		return r, e
	}
	if strings.TrimSpace(r.Text) == "" {
		return r, Empty
	}
	return r, nil
}
