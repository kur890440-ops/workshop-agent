//go:build linux || darwin || freebsd

package speech

import (
	"github.com/ebitengine/purego"
	"strings"
	"unsafe"
)

func loadORT(path string) (uintptr, func() error, error) {
	h, e := purego.Dlopen(path, purego.RTLD_NOW|purego.RTLD_LOCAL)
	if e != nil {
		return 0, nil, e
	}
	close := func() error { return purego.Dlclose(h) }
	symbol, e := purego.Dlsym(h, "OrtGetApiBase")
	if e != nil {
		close()
		return 0, nil, e
	}
	return symbol, close, nil
}
func ortPath(path string) (unsafe.Pointer, any, error) {
	if strings.ContainsRune(path, 0) {
		return nil, nil, InvalidAssets
	}
	p := append([]byte(path), 0)
	return unsafe.Pointer(&p[0]), p, nil
}
