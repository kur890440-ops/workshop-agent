package speech

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func frontendConfig() FrontendConfig {
	return FrontendConfig{16000, 320, 320, 160, 64, false, "htk", 2, "hann_periodic", "log_clamp_1e-9_1e9"}
}
func TestFrontendReferenceGolden(t *testing.T) {
	wav, e := os.ReadFile("testdata/frontend.wav")
	if e != nil {
		t.Fatal(e)
	}
	pcm, e := (Normalizer{1 << 20, time.Minute}).Normalize(context.Background(), AudioInput{Data: wav})
	if e != nil {
		t.Fatal(e)
	}
	got, frames, e := Features(context.Background(), pcm.Samples, frontendConfig())
	if e != nil {
		t.Fatal(e)
	}
	reference, e := os.ReadFile("testdata/frontend.f32")
	if e != nil {
		t.Fatal(e)
	}
	if frames != 99 || len(reference) != len(got)*4 {
		t.Fatal("shape mismatch")
	}
	maxDelta := 0.
	for i, v := range got {
		expected := math.Float32frombits(binary.LittleEndian.Uint32(reference[i*4:]))
		delta := math.Abs(float64(v - expected))
		maxDelta = max(maxDelta, delta)
		if delta > 0.0002 {
			t.Fatalf("feature %d delta %g expected %g got %g", i, delta, expected, v)
		}
	}
	t.Logf("reference max absolute delta: %.9g", maxDelta)
}
func TestCTCCollapseBlankAndLength(t *testing.T) {
	ids := []int{0, 0, 2, 0, 1, 1, 2, 1}
	logits := make([]float32, len(ids)*3)
	for i, k := range ids {
		logits[i*3+k] = 1
	}
	got, e := DecodeCTC(logits, len(ids), 3, 7, 2, []string{"а", "б"})
	if e != nil || got != "ааб" {
		t.Fatal(got, e)
	}
	if _, e = DecodeCTC(logits, 8, 3, 9, 2, []string{"а", "б"}); e != InvalidAssets {
		t.Fatal(e)
	}
}
func waveBytes(rate, channels, frames int) []byte {
	b := make([]byte, 44+frames*channels*2)
	copy(b, "RIFF")
	binary.LittleEndian.PutUint32(b[4:], uint32(len(b)-8))
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint16(b[20:], 1)
	binary.LittleEndian.PutUint16(b[22:], uint16(channels))
	binary.LittleEndian.PutUint32(b[24:], uint32(rate))
	binary.LittleEndian.PutUint32(b[28:], uint32(rate*channels*2))
	binary.LittleEndian.PutUint16(b[32:], uint16(channels*2))
	binary.LittleEndian.PutUint16(b[34:], 16)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], uint32(len(b)-44))
	for i := 0; i < frames; i++ {
		for c := 0; c < channels; c++ {
			binary.LittleEndian.PutUint16(b[44+(i*channels+c)*2:], uint16(8192+c*8192))
		}
	}
	return b
}
func TestAudioWAVMonoResampleAndLimits(t *testing.T) {
	n := Normalizer{1 << 20, time.Second}
	pcm, e := n.Normalize(context.Background(), AudioInput{Data: waveBytes(48000, 2, 4800)})
	if e != nil || len(pcm.Samples) != 1600 || pcm.SampleRate != 16000 {
		t.Fatal(len(pcm.Samples), e)
	}
	if math.Abs(float64(pcm.Samples[800])-.375) > 1e-5 {
		t.Fatal("mono amplitude", pcm.Samples[800])
	}
	for _, tc := range []struct {
		a    AudioInput
		n    Normalizer
		want Code
	}{{AudioInput{Data: waveBytes(16000, 1, 16001)}, n, TooLong}, {AudioInput{Data: []byte("bad")}, n, Unsupported}, {AudioInput{Data: make([]byte, 9)}, Normalizer{8, time.Second}, TooLarge}, {AudioInput{Data: waveBytes(16000, 1, 100), Duration: 2 * time.Second}, n, TooLong}} {
		_, e := tc.n.Normalize(context.Background(), tc.a)
		if e != tc.want {
			t.Fatal(e, tc.want)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e = n.Normalize(ctx, AudioInput{Data: waveBytes(48000, 1, 100)}); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
}
func oggPage(sequence uint32, flags byte, granule uint64, packet []byte) []byte {
	b := make([]byte, 28+len(packet))
	copy(b, "OggS")
	b[5] = flags
	binary.LittleEndian.PutUint64(b[6:], granule)
	binary.LittleEndian.PutUint32(b[14:], 1)
	binary.LittleEndian.PutUint32(b[18:], sequence)
	b[26] = 1
	b[27] = byte(len(packet))
	copy(b[28:], packet)
	binary.LittleEndian.PutUint32(b[22:], oggCRC(b))
	return b
}
func TestOggOpusDecodeAndCRC(t *testing.T) {
	head := make([]byte, 19)
	copy(head, "OpusHead")
	head[8] = 1
	head[9] = 1
	binary.LittleEndian.PutUint16(head[10:], 120)
	tags := make([]byte, 16)
	copy(tags, "OpusTags")
	b := append(oggPage(0, 2, 0, head), oggPage(1, 0, 0, tags)...)
	b = append(b, oggPage(2, 4, 960, []byte{0xf8, 0xff, 0xfe})...)
	n := Normalizer{1 << 20, time.Second}
	pcm, e := n.Normalize(context.Background(), AudioInput{Data: b})
	if e != nil || len(pcm.Samples) != 280 {
		t.Fatal(len(pcm.Samples), e)
	}
	b[len(b)-1] ^= 1
	if _, e = n.Normalize(context.Background(), AudioInput{Data: b}); e != Unsupported {
		t.Fatal(e)
	}
}
func fakeAssets(t *testing.T) Config {
	t.Helper()
	c := DefaultConfig()
	c.Enabled = true
	c.ModelPath = filepath.Join(t.TempDir(), "модель")
	if e := os.MkdirAll(c.ModelPath, 0755); e != nil {
		t.Fatal(e)
	}
	a := Assets{Version: 1, Model: "v3_ctc", Frontend: frontendConfig(), BlankID: 33, Tokenizer: "charwise", Inputs: []string{"features", "feature_lengths"}, Outputs: []string{"log_probs", "encoded_lengths"}}
	vocab := strings.Split(" абвгдежзийклмнопрстуфхцчшщъыьэюя", "")
	ab, _ := json.Marshal(a)
	vb, _ := json.Marshal(vocab)
	files := map[string][]byte{"model.onnx": []byte("fake"), "config.json": ab, "vocab.json": vb}
	manifest := map[string]string{}
	for name, b := range files {
		if e := os.WriteFile(filepath.Join(c.ModelPath, name), b, 0600); e != nil {
			t.Fatal(e)
		}
		sum := sha256.Sum256(b)
		manifest[name] = hex.EncodeToString(sum[:])
	}
	mb, _ := json.Marshal(manifest)
	os.WriteFile(filepath.Join(c.ModelPath, "manifest.json"), mb, 0600)
	return c
}

type fakeEngine struct {
	wait, empty bool
	calls       int
	closed      bool
}

func (f *fakeEngine) Infer(ctx context.Context, _ []float32, _ int) ([]float32, int, int, int, error) {
	f.calls++
	if f.wait {
		<-ctx.Done()
		return nil, 0, 0, 0, ctx.Err()
	}
	out := make([]float32, 34)
	if f.empty {
		out[33] = 1
	} else {
		out[1] = 1
	}
	return out, 1, 34, 1, nil
}
func (f *fakeEngine) Close() error { f.closed = true; return nil }
func TestProviderAssetsFallbackSuccessEmptyTimeout(t *testing.T) {
	c := fakeAssets(t)
	f := &fakeEngine{}
	var devices []string
	p := newProvider(c, Normalizer{c.MaxBytes, c.MaxDuration}, func(_ Config, _ Assets, d string) (Engine, error) {
		devices = append(devices, d)
		if d == "cuda" {
			return nil, RuntimeUnavailable
		}
		return f, nil
	})
	if strings.Join(devices, ",") != "cuda,cpu" || p.Status(context.Background()).Device != "cpu" {
		t.Fatal(devices)
	}
	input := AudioInput{Data: waveBytes(16000, 1, 1600)}
	for i := 0; i < 2; i++ {
		r, e := p.Transcribe(context.Background(), input)
		if e != nil || r.Text != "а" || r.Status != Success {
			t.Fatal(r, e)
		}
	}
	if f.calls != 2 {
		t.Fatal(f.calls)
	}
	f.empty = true
	if _, e := p.Transcribe(context.Background(), input); e != Empty {
		t.Fatal(e)
	}
	f.empty = false
	f.wait = true
	p.cfg.Timeout = 5 * time.Millisecond
	if r, e := p.Transcribe(context.Background(), input); !errors.Is(e, context.DeadlineExceeded) || r.ErrorCode != Timeout {
		t.Fatal(r, e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := p.Transcribe(ctx, input); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	p.Close()
	if !f.closed {
		t.Fatal("session not closed")
	}
	c.ModelPath = t.TempDir()
	missing := New(c)
	if missing.Status(ctx).LastError != ModelUnavailable {
		t.Fatal(missing.Status(ctx))
	}
	c = fakeAssets(t)
	os.WriteFile(filepath.Join(c.ModelPath, "model.onnx"), []byte("tamper"), 0600)
	if New(c).Status(ctx).LastError != InvalidAssets {
		t.Fatal("hash not checked")
	}
}
func TestNativeONNXOptional(t *testing.T) {
	dll := os.Getenv("SPEECH_TEST_RUNTIME")
	dir := os.Getenv("SPEECH_TEST_NATIVE_MODEL")
	if dll == "" || dir == "" {
		t.Skip("set SPEECH_TEST_RUNTIME and SPEECH_TEST_NATIVE_MODEL")
	}
	c := DefaultConfig()
	c.Enabled = true
	c.RuntimePath = dll
	c.ModelPath = dir
	c.Device = "auto"
	p := New(c)
	defer p.Close()
	if !p.Status(context.Background()).ModelLoaded {
		t.Fatal(p.Status(context.Background()))
	}
	for i := 0; i < 3; i++ {
		r, e := p.Transcribe(context.Background(), AudioInput{Data: waveBytes(16000, 1, 1600)})
		if e != nil || r.Text != "аа" {
			t.Fatal(r, e)
		}
	}
}
func TestRealGigaAMOptional(t *testing.T) {
	dir := os.Getenv("SPEECH_TEST_MODEL")
	wav := os.Getenv("SPEECH_TEST_AUDIO")
	if dir == "" || wav == "" {
		t.Skip("real GigaAM assets/sample not configured")
	}
	c := DefaultConfig()
	c.Enabled = true
	c.ModelPath = dir
	c.RuntimePath = os.Getenv("SPEECH_TEST_RUNTIME")
	p := New(c)
	defer p.Close()
	b, e := os.ReadFile(wav)
	if e != nil {
		t.Fatal(e)
	}
	engine := p.engine.(*onnxEngine)
	session := engine.session
	for call := 1; call <= 2; call++ {
		r, err := p.Transcribe(context.Background(), AudioInput{Data: b, FileName: wav})
		if err != nil {
			t.Fatal(r, err)
		}
		if expected := os.Getenv("SPEECH_TEST_EXPECTED"); expected != "" && r.Text != expected {
			t.Fatal(r.Text, expected)
		}
		if p.engine != engine || engine.session != session || session == 0 {
			t.Fatal("native session recreated")
		}
		t.Logf("call=%d same_native_session=true text=%q duration=%v total=%v RTF=%.3f timings=%+v", call, r.Text, r.AudioDuration, r.ProcessingDuration, float64(r.ProcessingDuration)/float64(r.AudioDuration), r.Timings)
	}
	if path := os.Getenv("SPEECH_TEST_FEATURES"); path != "" {
		pcm, err := p.normalizer.Normalize(context.Background(), AudioInput{Data: b})
		if err != nil {
			t.Fatal(err)
		}
		features, _, err := Features(context.Background(), pcm.Samples, p.assets.Frontend)
		if err != nil {
			t.Fatal(err)
		}
		reference, err := os.ReadFile(path)
		if err != nil || len(reference) != 4*len(features) {
			t.Fatal("invalid reference", err)
		}
		maxError := 0.0
		worst := 0
		for i, value := range features {
			delta := math.Abs(float64(value - math.Float32frombits(binary.LittleEndian.Uint32(reference[i*4:]))))
			if delta > maxError {
				maxError, worst = delta, i
			}
		}
		// Quiet speech bins amplify float32 FFT rounding after log. Official
		// torch float32 vs float64 itself differs by 0.000643 on this sample.
		// 0.001 log units bounds relative spectral-power error to about 0.1%.
		if maxError > 0.001 {
			t.Fatal("real frontend mismatch", maxError, "index", worst, "go", features[worst], "reference", math.Float32frombits(binary.LittleEndian.Uint32(reference[worst*4:])))
		}
		t.Logf("real frontend max_absolute_error=%g", maxError)
	}
}

func TestNativeCancellationOptional(t *testing.T) {
	dir := os.Getenv("SPEECH_TEST_CANCEL_MODEL")
	dll := os.Getenv("SPEECH_TEST_RUNTIME")
	if dir == "" || dll == "" {
		t.Skip("set SPEECH_TEST_CANCEL_MODEL and SPEECH_TEST_RUNTIME")
	}
	c := DefaultConfig()
	c.Enabled = true
	c.Device = "cpu"
	c.ModelPath = dir
	c.RuntimePath = dll
	c.Timeout = 20 * time.Millisecond
	p := New(c)
	defer p.Close()
	if !p.Status(context.Background()).ModelLoaded {
		t.Fatal(p.Status(context.Background()))
	}
	for i := 0; i < 2; i++ {
		started := time.Now()
		r, e := p.Transcribe(context.Background(), AudioInput{Data: waveBytes(16000, 1, 1600)})
		if !errors.Is(e, context.DeadlineExceeded) || r.ErrorCode != Timeout || time.Since(started) > time.Second {
			t.Fatal(r, e, time.Since(started))
		}
	}
}
