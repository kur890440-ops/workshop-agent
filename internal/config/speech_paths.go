package config

import (
	"os"
	"path/filepath"
	"strings"
	"workshop-agent/internal/speech"
)

// SpeechPaths contains only safe startup diagnostics, never application credentials.
type SpeechPaths struct {
	CWD, Executable, AppRoot           string
	RawModelPath, RawRuntimePath       string
	ModelPath, RuntimePath             string
	ModelPathExists, RuntimePathExists bool
}

// Installed layouts: app/executable or app/bin/executable. go run uses repo cwd.
func applicationRoot(cwd, executable string) string {
	dir := filepath.Dir(executable)
	if strings.EqualFold(filepath.Base(dir), "bin") {
		return filepath.Dir(dir)
	}
	for _, marker := range []string{".env", "assets", "go.mod"} {
		if _, err := os.Stat(filepath.Join(dir, marker)); err == nil {
			return dir
		}
	}
	if _, err := os.Stat(filepath.Join(cwd, "go.mod")); err == nil {
		return cwd
	}
	return dir
}

func resolveSpeechPaths(c *speech.Config, cwd, executable string) SpeechPaths {
	root := applicationRoot(cwd, executable)
	resolve := func(path string) string {
		if filepath.IsAbs(path) {
			return filepath.Clean(path)
		}
		// Do not reinterpret current-drive rooted Windows paths as app-relative.
		// filepath.Abs preserves their existing Windows root semantics.
		if strings.HasPrefix(path, "/") || strings.HasPrefix(path, `\`) {
			if absolute, err := filepath.Abs(path); err == nil {
				return absolute
			}
		}
		return filepath.Join(root, path)
	}
	p := SpeechPaths{CWD: cwd, Executable: executable, AppRoot: root, RawModelPath: c.ModelPath, RawRuntimePath: c.RuntimePath}
	c.ModelPath, c.RuntimePath = resolve(c.ModelPath), resolve(c.RuntimePath)
	p.ModelPath, p.RuntimePath = c.ModelPath, c.RuntimePath
	if st, err := os.Stat(c.ModelPath); err == nil {
		p.ModelPathExists = st.IsDir()
	}
	if st, err := os.Stat(c.RuntimePath); err == nil {
		p.RuntimePathExists = !st.IsDir()
	}
	return p
}
