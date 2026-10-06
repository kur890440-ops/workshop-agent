package speech

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestProductionSpeechHasNoSubprocess(t *testing.T) {
	paths, e := filepath.Glob("*.go")
	if e != nil {
		t.Fatal(e)
	}
	paths = append(paths, "../telegram/speech.go")
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		f, e := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if e != nil {
			t.Fatal(e)
		}
		for _, i := range f.Imports {
			v, _ := strconv.Unquote(i.Path.Value)
			if v == "os/exec" || strings.HasPrefix(v, "workshop-agent/internal/integrations") {
				t.Fatalf("unexpected speech dependency %s in %s", v, path)
			}
		}
	}
	b, e := os.ReadFile("../telegram/speech.go")
	if e != nil {
		t.Fatal(e)
	}
	if strings.Count(string(b), "b.handleMessage(&m)") != 1 {
		t.Fatal("speech must deliver once through existing handler")
	}
}
