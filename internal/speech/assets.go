package speech

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
)

type Assets struct {
	Version   int            `json:"version"`
	Model     string         `json:"model"`
	Frontend  FrontendConfig `json:"frontend"`
	BlankID   int            `json:"blank_id"`
	Tokenizer string         `json:"tokenizer"`
	Inputs    []string       `json:"inputs"`
	Outputs   []string       `json:"outputs"`
	Vocab     []string       `json:"-"`
}

func readAssets(dir string) (Assets, error) {
	var a Assets
	var manifest map[string]string
	b, e := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if os.IsNotExist(e) {
		return a, ModelUnavailable
	}
	if e != nil || json.Unmarshal(b, &manifest) != nil {
		return a, InvalidAssets
	}
	for _, name := range []string{"model.onnx", "config.json", "vocab.json"} {
		expected, ok := manifest[name]
		if !ok || len(expected) != 64 {
			return a, InvalidAssets
		}
		f, e := os.Open(filepath.Join(dir, name))
		if os.IsNotExist(e) {
			return a, ModelUnavailable
		}
		if e != nil {
			return a, InvalidAssets
		}
		h := sha256.New()
		_, e = io.Copy(h, f)
		f.Close()
		if e != nil || hex.EncodeToString(h.Sum(nil)) != expected {
			return a, InvalidAssets
		}
	}
	b, e = os.ReadFile(filepath.Join(dir, "config.json"))
	if e != nil || json.Unmarshal(b, &a) != nil {
		return a, InvalidAssets
	}
	b, e = os.ReadFile(filepath.Join(dir, "vocab.json"))
	if e != nil || json.Unmarshal(b, &a.Vocab) != nil {
		return a, InvalidAssets
	}
	if a.Version != 1 || a.Model != "v3_ctc" || !a.Frontend.valid() || a.Tokenizer != "charwise" || len(a.Vocab) != 33 || a.BlankID != len(a.Vocab) || len(a.Inputs) != 2 || len(a.Outputs) != 2 || a.Inputs[0] == a.Inputs[1] || a.Outputs[0] == a.Outputs[1] {
		return a, InvalidAssets
	}
	for _, v := range append(append([]string{}, a.Inputs...), a.Outputs...) {
		if v == "" {
			return a, InvalidAssets
		}
	}
	return a, nil
}
