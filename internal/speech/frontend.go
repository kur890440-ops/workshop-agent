package speech

import (
	"context"
	"gonum.org/v1/gonum/dsp/fourier"
	"math"
	"strings"
)

// FrontendConfig is exported from the official model config, not inferred from the filename.
type FrontendConfig struct {
	SampleRate    int     `json:"sample_rate"`
	FFT           int     `json:"n_fft"`
	Window        int     `json:"win_length"`
	Hop           int     `json:"hop_length"`
	Mels          int     `json:"features"`
	Center        bool    `json:"center"`
	MelScale      string  `json:"mel_scale"`
	Power         float64 `json:"power"`
	WindowType    string  `json:"window"`
	Normalization string  `json:"normalization"`
}

func (c FrontendConfig) valid() bool {
	return c.SampleRate == 16000 && c.FFT == 320 && c.Window == 320 && c.Hop == 160 && c.Mels == 64 && !c.Center && c.MelScale == "htk" && c.Power == 2 && c.WindowType == "hann_periodic" && c.Normalization == "log_clamp_1e-9_1e9"
}

// Features matches torchaudio MelSpectrogram (HTK, norm=None, power=2,
// periodic Hann, center=False) followed by GigaAM SpecScaler. Layout: [1,mel,time].
func Features(ctx context.Context, pcm []float32, c FrontendConfig) ([]float32, int, error) {
	if !c.valid() {
		return nil, 0, InvalidAssets
	}
	if len(pcm) < c.FFT {
		return nil, 0, Unsupported
	}
	frames := 1 + (len(pcm)-c.FFT)/c.Hop
	out := make([]float32, c.Mels*frames)
	fft := fourier.NewFFT(c.FFT)
	window := make([]float64, c.FFT)
	frame := make([]float64, c.FFT)
	for i := range window {
		window[i] = 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(c.Window))
	}
	bins := c.FFT/2 + 1
	filters := make([]float64, c.Mels*bins)
	// Match torchaudio's float32 mel grid and triangular filters.
	melMax := float32(2595 * math.Log10(1+float64(c.SampleRate)/2/700))
	edges := make([]float32, c.Mels+2)
	for i := range edges {
		m := float32(i) * melMax / float32(c.Mels+1)
		edges[i] = 700 * (float32(math.Pow(10, float64(m/2595))) - 1)
	}
	for m := 0; m < c.Mels; m++ {
		for k := 0; k < bins; k++ {
			f := float32(k) * float32(c.SampleRate/2) / float32(bins-1)
			a := (f - edges[m]) / (edges[m+1] - edges[m])
			b := (edges[m+2] - f) / (edges[m+2] - edges[m+1])
			filters[m*bins+k] = float64(max(float32(0), min(a, b)))
		}
	}
	for t := 0; t < frames; t++ {
		if t%64 == 0 && ctx.Err() != nil {
			return nil, 0, ctx.Err()
		}
		for i := range frame {
			frame[i] = float64(pcm[t*c.Hop+i]) * window[i]
		}
		spectrum := fft.Coefficients(nil, frame)
		for m := 0; m < c.Mels; m++ {
			sum := 0.
			for k, z := range spectrum {
				sum += (real(z)*real(z) + imag(z)*imag(z)) * filters[m*bins+k]
			}
			out[m*frames+t] = float32(math.Log(max(1e-9, min(1e9, sum))))
		}
	}
	return out, frames, nil
}
func DecodeCTC(logits []float32, frames, classes, length, blank int, vocab []string) (string, error) {
	if classes != len(vocab)+1 || blank != len(vocab) || frames < 0 || length < 0 || length > frames || len(logits) != frames*classes {
		return "", InvalidAssets
	}
	var b strings.Builder
	previous := -1
	for t := 0; t < length; t++ {
		best := 0
		for i := 0; i < classes; i++ {
			v := logits[t*classes+i]
			if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
				return "", Failed
			}
			if v > logits[t*classes+best] {
				best = i
			}
		}
		if best != blank && best != previous {
			b.WriteString(vocab[best])
		}
		previous = best
	}
	return strings.TrimSpace(b.String()), nil
}
