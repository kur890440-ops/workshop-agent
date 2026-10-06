package speech

import (
	"context"
	"encoding/binary"
	"testing"
	"time"
)

func TestOggOpusEOFWithoutEOS(t *testing.T) {
	head := make([]byte, 19)
	copy(head, "OpusHead")
	head[8] = 1
	head[9] = 1
	binary.LittleEndian.PutUint16(head[10:], 312)
	tags := make([]byte, 16)
	copy(tags, "OpusTags")
	headers := append(oggPage(0, 2, 0, head), oggPage(1, 0, 0, tags)...)
	audio := []byte{0xf8, 0xff, 0xfe}
	stream := func(tail []byte) []byte { return append(append([]byte{}, headers...), tail...) }
	n := Normalizer{1 << 20, time.Second}
	for _, tc := range []struct {
		name string
		tail []byte
		want int
		fail bool
	}{
		{"clean_eof", oggPage(2, 0, 960, audio), 216, false},
		{"eos_trimming", oggPage(2, 4, 912, audio), 200, false},
		{"truncated_page", oggPage(2, 0, 960, audio)[:30], 0, true},
		{"unfinished_packet", oggPage(2, 0, ^uint64(0), make([]byte, 255)), 0, true},
		{"no_audio", nil, 0, true},
		{"invalid_eos_granule", oggPage(2, 4, 961, audio), 0, true},
		{"eos_before_preskip", oggPage(2, 4, 300, audio), 0, true},
		{"after_eos", append(oggPage(2, 4, 960, audio), oggPage(3, 0, 1920, audio)...), 0, true},
		{"sequence_gap", oggPage(3, 0, 960, audio), 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pcm, err := n.Normalize(context.Background(), AudioInput{Data: stream(tc.tail)})
			if tc.fail {
				if ErrorCode(err) != Unsupported {
					t.Fatal(err)
				}
				return
			}
			if err != nil || len(pcm.Samples) != tc.want {
				t.Fatal(len(pcm.Samples), err)
			}
		})
	}
	corrupt := stream(oggPage(2, 0, 960, audio))
	corrupt[len(corrupt)-1] ^= 1
	if _, err := n.Normalize(context.Background(), AudioInput{Data: corrupt}); ErrorCode(err) != Unsupported {
		t.Fatal("CRC bypassed", err)
	}
}
