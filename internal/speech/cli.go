package speech

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// RunCLI is part of workshop-agent.exe. It does not open the application DB or Telegram.
func RunCLI(ctx context.Context, c Config, args []string, w io.Writer) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: speech status | speech test <file> | speech day26 <A.wav> <B.wav> <C.wav>")
	}
	if args[0] != "status" {
		c.Enabled = true
	}
	p := New(c)
	defer p.Close()
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if len(args) == 1 && args[0] == "status" {
		return enc.Encode(p.Status(ctx))
	}
	if !(args[0] == "test" && len(args) == 2 || args[0] == "day26" && len(args) == 4) {
		return fmt.Errorf("usage: speech test <file> | speech day26 <A> <B> <C>")
	}
	failed := false
	for i, path := range args[1:] {
		input := AudioInput{FileName: filepath.Base(path), Source: "cli"}
		f, e := os.Open(path)
		if e == nil {
			input.Data, e = io.ReadAll(io.LimitReader(f, c.MaxBytes+1))
			f.Close()
		}
		var r SpeechRecognitionResult
		if e == nil {
			r, e = p.Transcribe(ctx, input)
		} else {
			r.Status = Unsupported
			r.ErrorCode = Unsupported
		}
		if e != nil {
			failed = true
		}
		rtf := 0.
		if r.AudioDuration > 0 {
			rtf = float64(r.ProcessingDuration) / float64(r.AudioDuration)
		}
		if err := enc.Encode(struct {
			Sample         string
			Result         SpeechRecognitionResult
			RealTimeFactor float64
		}{fmt.Sprintf("%c: %s", 'A'+i, filepath.Base(path)), r, rtf}); err != nil {
			return err
		}
	}
	if failed {
		return fmt.Errorf("one or more speech samples failed; inspect status")
	}
	return nil
}
