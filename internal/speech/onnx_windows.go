//go:build windows

package speech

import (
	"golang.org/x/sys/windows"
	"unsafe"
)

func loadORT(path string) (uintptr, func() error, error) {
	h, e := windows.LoadLibraryEx(path, 0, windows.LOAD_LIBRARY_SEARCH_DLL_LOAD_DIR|windows.LOAD_LIBRARY_SEARCH_DEFAULT_DIRS)
	if e != nil {
		return 0, nil, e
	}
	close := func() error { return windows.FreeLibrary(h) }
	symbol, e := windows.GetProcAddress(h, "OrtGetApiBase")
	if e != nil {
		close()
		return 0, nil, e
	}
	return symbol, close, nil
}
func ortPath(path string) (unsafe.Pointer, any, error) {
	p, e := windows.UTF16FromString(path)
	if e != nil {
		return nil, nil, e
	}
	return unsafe.Pointer(&p[0]), p, nil
}
