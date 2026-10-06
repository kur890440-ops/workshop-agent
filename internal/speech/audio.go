package speech

import (
	"bytes"
	"context"
	"encoding/binary"
	"github.com/hajimehoshi/go-mp3"
	"github.com/pion/opus"
	"io"
	"log/slog"
	"math"
	"strings"
	"time"
)

type Normalizer struct {
	MaxBytes    int64
	MaxDuration time.Duration
}

func (n Normalizer) Normalize(ctx context.Context, a AudioInput) (PCM, error) {
	if ctx.Err() != nil {
		return PCM{}, ctx.Err()
	}
	if int64(len(a.Data)) > n.MaxBytes {
		return PCM{}, TooLarge
	}
	if a.Duration > n.MaxDuration {
		return PCM{}, TooLong
	}
	var samples []float32
	var rate int
	var err error
	switch {
	case len(a.Data) >= 12 && string(a.Data[:4]) == "RIFF" && string(a.Data[8:12]) == "WAVE":
		samples, rate, err = n.wav(ctx, a.Data)
	case bytes.HasPrefix(a.Data, []byte("OggS")):
		samples, rate, err = n.ogg(ctx, a.Data)
	case strings.EqualFold(a.MimeType, "audio/mpeg") || strings.HasSuffix(strings.ToLower(a.FileName), ".mp3") || bytes.HasPrefix(a.Data, []byte("ID3")):
		samples, rate, err = n.mp3(ctx, a.Data)
	default:
		slog.Warn("speech audio decode rejected", "reason", "unrecognized_container", "audio_bytes", len(a.Data))
		return PCM{}, Unsupported
	}
	if err != nil {
		return PCM{}, err
	}
	if len(samples) == 0 {
		return PCM{}, Unsupported
	}
	if float64(len(samples))/float64(rate) > n.MaxDuration.Seconds() {
		return PCM{}, TooLong
	}
	samples, err = resample(ctx, samples, rate, 16000)
	return PCM{samples, 16000}, err
}
func (n Normalizer) wav(ctx context.Context, b []byte) ([]float32, int, error) {
	if uint64(binary.LittleEndian.Uint32(b[4:8]))+8 != uint64(len(b)) {
		return nil, 0, Unsupported
	}
	var format, channels, bits, align uint16
	var rate uint32
	var data []byte
	for pos := 12; pos+8 <= len(b); {
		size := uint64(binary.LittleEndian.Uint32(b[pos+4:]))
		start := pos + 8
		if size > uint64(len(b)-start) {
			return nil, 0, Unsupported
		}
		chunk := b[start : start+int(size)]
		switch string(b[pos : pos+4]) {
		case "fmt ":
			if len(chunk) < 16 {
				return nil, 0, Unsupported
			}
			format = binary.LittleEndian.Uint16(chunk)
			channels = binary.LittleEndian.Uint16(chunk[2:])
			rate = binary.LittleEndian.Uint32(chunk[4:])
			align = binary.LittleEndian.Uint16(chunk[12:])
			bits = binary.LittleEndian.Uint16(chunk[14:])
		case "data":
			if data != nil {
				return nil, 0, Unsupported
			}
			data = chunk
		}
		pos = start + int(size) + int(size%2)
	}
	if channels < 1 || channels > 8 || rate < 8000 || rate > 192000 || (format != 1 && format != 3) || (bits != 8 && bits != 16 && bits != 24 && bits != 32) || format == 3 && bits != 32 || align != channels*(bits/8) || len(data)%int(align) != 0 {
		return nil, 0, Unsupported
	}
	count := len(data) / int(align)
	if float64(count)/float64(rate) > n.MaxDuration.Seconds() {
		return nil, 0, TooLong
	}
	out := make([]float32, count)
	for i := range out {
		if i%4096 == 0 && ctx.Err() != nil {
			return nil, 0, ctx.Err()
		}
		sum := float64(0)
		for c := 0; c < int(channels); c++ {
			p := data[i*int(align)+c*int(bits/8):]
			var v float64
			switch {
			case format == 3:
				v = float64(math.Float32frombits(binary.LittleEndian.Uint32(p)))
			case bits == 8:
				v = float64(int(p[0])-128) / 128
			case bits == 16:
				v = float64(int16(binary.LittleEndian.Uint16(p))) / 32768
			case bits == 24:
				v = float64(int32(uint32(p[0])<<8|uint32(p[1])<<16|uint32(p[2])<<24)>>8) / 8388608
			case bits == 32:
				v = float64(int32(binary.LittleEndian.Uint32(p))) / 2147483648
			}
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return nil, 0, Unsupported
			}
			sum += v
		}
		out[i] = float32(sum / float64(channels))
	}
	return out, int(rate), nil
}

// Ogg lacing is reconstructed across pages. Chained/multiplexed streams and
// multichannel mappings are rejected; Telegram mapping-family 0 is supported.
func (n Normalizer) ogg(ctx context.Context, b []byte) ([]float32, int, error) {
	var serial, seq uint32
	var packet []byte
	var out []float32
	var decoder opus.Decoder
	packets := 0
	skip := 0
	gain := float32(1)
	eos := false
	var final uint64
	for pos := 0; pos < len(b); {
		if ctx.Err() != nil {
			return nil, 0, ctx.Err()
		}
		if eos || len(b)-pos < 27 || string(b[pos:pos+4]) != "OggS" || b[pos+4] != 0 {
			slog.Warn("speech audio decode rejected", "container", "ogg/opus", "reason", "page_header_or_data_after_eos", "packet_index", packets)
			return nil, 0, Unsupported
		}
		segs := int(b[pos+26])
		if len(b)-pos < 27+segs {
			slog.Warn("speech audio decode rejected", "container", "ogg/opus", "reason", "truncated_lacing_table", "packet_index", packets)
			return nil, 0, Unsupported
		}
		size := 0
		for _, l := range b[pos+27 : pos+27+segs] {
			size += int(l)
		}
		end := pos + 27 + segs + size
		if end > len(b) {
			slog.Warn("speech audio decode rejected", "container", "ogg/opus", "reason", "truncated_page_body", "packet_index", packets)
			return nil, 0, Unsupported
		}
		page := b[pos:end]
		if oggCRC(page) != binary.LittleEndian.Uint32(page[22:]) {
			slog.Warn("speech audio decode rejected", "container", "ogg/opus", "reason", "page_crc_mismatch", "packet_index", packets)
			return nil, 0, Unsupported
		}
		s := binary.LittleEndian.Uint32(page[14:])
		q := binary.LittleEndian.Uint32(page[18:])
		flags := page[5]
		if pos == 0 {
			serial = s
			seq = q
			if flags&2 == 0 {
				slog.Warn("speech audio decode rejected", "container", "ogg/opus", "reason", "missing_bos", "packet_index", packets)
				return nil, 0, Unsupported
			}
		} else {
			if s != serial || q != seq+1 || flags&2 != 0 {
				slog.Warn("speech audio decode rejected", "container", "ogg/opus", "reason", "serial_or_sequence_or_bos", "packet_index", packets)
				return nil, 0, Unsupported
			}
			seq = q
		}
		if (flags&1 != 0) != (len(packet) > 0) {
			slog.Warn("speech audio decode rejected", "container", "ogg/opus", "reason", "continuation_flag_mismatch", "packet_index", packets)
			return nil, 0, Unsupported
		}
		cursor := 27 + segs
		for _, l := range page[27 : 27+segs] {
			packet = append(packet, page[cursor:cursor+int(l)]...)
			cursor += int(l)
			if len(packet) > 65536 {
				slog.Warn("speech audio decode rejected", "container", "ogg/opus", "reason", "packet_size_limit", "packet_index", packets)
				return nil, 0, Unsupported
			}
			if l == 255 {
				continue
			}
			switch packets {
			case 0:
				if len(packet) != 19 || string(packet[:8]) != "OpusHead" || packet[8] != 1 || (packet[9] != 1 && packet[9] != 2) || packet[18] != 0 {
					slog.Warn("speech audio decode rejected", "container", "ogg/opus", "reason", "opus_identification_header", "header_bytes", len(packet))
					return nil, 0, Unsupported
				}
				skip = int(binary.LittleEndian.Uint16(packet[10:]))
				gain = float32(math.Pow(10, float64(int16(binary.LittleEndian.Uint16(packet[16:])))/5120))
				var e error
				decoder, e = opus.NewDecoderWithOutput(48000, 1)
				if e != nil {
					slog.Warn("speech audio decode rejected", "container", "ogg/opus", "reason", "opus_decoder_initialization", "packet_index", packets)
					return nil, 0, Unsupported
				}
			case 1:
				if len(packet) < 16 || string(packet[:8]) != "OpusTags" {
					slog.Warn("speech audio decode rejected", "container", "ogg/opus", "reason", "opus_tags_header", "packet_index", packets)
					return nil, 0, Unsupported
				}
			default:
				buf := make([]float32, 5760)
				count, e := decoder.DecodeToFloat32(packet, buf)
				if e != nil {
					slog.Warn("speech audio decode rejected", "container", "ogg/opus", "reason", "opus_packet_decode", "packet_index", packets, "packet_bytes", len(packet), "decoder_error", e.Error())
					return nil, 0, Unsupported
				}
				out = append(out, buf[:count]...)
				if len(out) > int(n.MaxDuration.Seconds()*48000)+skip+5760 {
					return nil, 0, TooLong
				}
			}
			packets++
			packet = nil
		}
		if flags&4 != 0 {
			eos = true
			final = binary.LittleEndian.Uint64(page[6:])
		}
		pos = end
	}
	// RFC 7845 section 3 requires handling streams without an EOS page.
	// At a clean page/packet boundary keep decoded samples; only an explicit
	// EOS granule provides end trimming. Never repair a partial page/packet.
	if !eos {
		final = uint64(len(out))
	}
	if len(packet) != 0 || packets < 3 || final > uint64(len(out)) || final < uint64(skip) {
		slog.Warn("speech audio decode rejected", "container", "ogg/opus", "reason", "eos_or_granule_bounds", "eos", eos, "pending_packet_bytes", len(packet), "packet_count", packets, "granule", final, "decoded_samples", len(out), "preskip", skip)
		return nil, 0, Unsupported
	}
	if !eos {
		slog.Info("speech audio EOF accepted", "container", "ogg/opus", "eos", false, "decoded_samples", len(out), "preskip", skip)
	}
	out = out[skip:int(final)]
	for i := range out {
		out[i] *= gain
	}
	return out, 48000, nil
}
func oggCRC(b []byte) uint32 {
	var crc uint32
	for i, v := range b {
		if i >= 22 && i < 26 {
			v = 0
		}
		crc ^= uint32(v) << 24
		for j := 0; j < 8; j++ {
			if crc&0x80000000 != 0 {
				crc = crc<<1 ^ 0x04c11db7
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}
func (n Normalizer) mp3(ctx context.Context, b []byte) ([]float32, int, error) {
	d, e := mp3.NewDecoder(bytes.NewReader(b))
	if e != nil {
		return nil, 0, Unsupported
	}
	rate := d.SampleRate()
	var out []float32
	buf := make([]byte, 4096)
	for {
		if ctx.Err() != nil {
			return nil, 0, ctx.Err()
		}
		count, e := io.ReadFull(d, buf)
		if count%4 != 0 {
			return nil, 0, Unsupported
		}
		for i := 0; i < count; i += 4 {
			out = append(out, (float32(int16(binary.LittleEndian.Uint16(buf[i:])))+float32(int16(binary.LittleEndian.Uint16(buf[i+2:]))))/65536)
		}
		if float64(len(out))/float64(rate) > n.MaxDuration.Seconds() {
			return nil, 0, TooLong
		}
		if e == io.EOF || e == io.ErrUnexpectedEOF {
			break
		}
		if e != nil {
			return nil, 0, Unsupported
		}
	}
	return out, rate, nil
}

// Windowed sinc low-pass resampling prevents aliasing on downsampling. This is
// audio normalization, separate from the model's exact spectral frontend.
func resample(ctx context.Context, in []float32, from, to int) ([]float32, error) {
	if from == to {
		return in, nil
	}
	count := int(int64(len(in)) * int64(to) / int64(from))
	out := make([]float32, count)
	cutoff := math.Min(1, float64(to)/float64(from)) * 0.95
	radius := 32 / cutoff
	for i := range out {
		if i%1024 == 0 && ctx.Err() != nil {
			return nil, ctx.Err()
		}
		x := float64(i) * float64(from) / float64(to)
		sum, weight := 0., 0.
		for j := int(math.Ceil(x - radius)); j <= int(math.Floor(x+radius)); j++ {
			if j < 0 || j >= len(in) {
				continue
			}
			delta := x - float64(j)
			z := math.Pi * delta * cutoff
			w := cutoff
			if math.Abs(z) > 1e-12 {
				w *= math.Sin(z) / z
			}
			w *= 0.5 + 0.5*math.Cos(math.Pi*delta/radius)
			sum += float64(in[j]) * w
			weight += w
		}
		if weight != 0 {
			out[i] = float32(sum / weight)
		}
	}
	return out, nil
}
