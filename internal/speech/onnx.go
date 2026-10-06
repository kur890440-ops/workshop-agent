package speech

import (
	"context"
	"fmt"
	"github.com/ebitengine/purego"
	"path/filepath"
	"runtime"
	"sync"
	"unsafe"
)

// Minimal ONNX Runtime C API binding. Slots are zero-based offsets from the
// append-only OrtApi in Microsoft's v1.23.2 onnxruntime_c_api.h (ORT_API_VERSION=23).
// Only tensor inference is bound; no server, Python, CGO or subprocess is involved.
type ortAPI struct {
	errorMessage                                                                                                     func(uintptr) string
	createEnv                                                                                                        func(int32, *byte, *uintptr) uintptr
	createSession                                                                                                    func(uintptr, unsafe.Pointer, uintptr, *uintptr) uintptr
	run                                                                                                              func(uintptr, uintptr, **byte, *uintptr, uintptr, **byte, uintptr, *uintptr) uintptr
	createOptions                                                                                                    func(*uintptr) uintptr
	graph                                                                                                            func(uintptr, int32) uintptr
	threads                                                                                                          func(uintptr, int32) uintptr
	createRun                                                                                                        func(*uintptr) uintptr
	terminate                                                                                                        func(uintptr) uintptr
	tensor                                                                                                           func(uintptr, unsafe.Pointer, uintptr, *int64, uintptr, int32, *uintptr) uintptr
	data                                                                                                             func(uintptr, *unsafe.Pointer) uintptr
	elementType                                                                                                      func(uintptr, *int32) uintptr
	dimensionsCount                                                                                                  func(uintptr, *uintptr) uintptr
	dimensions                                                                                                       func(uintptr, *int64, uintptr) uintptr
	shape                                                                                                            func(uintptr, *uintptr) uintptr
	memory                                                                                                           func(int32, int32, *uintptr) uintptr
	releaseEnv, releaseStatus, releaseMemory, releaseSession, releaseValue, releaseRun, releaseShape, releaseOptions func(uintptr)
	cudaCreate                                                                                                       func(*uintptr) uintptr
	cudaAppend                                                                                                       func(uintptr, uintptr) uintptr
	cudaRelease                                                                                                      func(uintptr)
}

func (a *ortAPI) check(status uintptr) error {
	if status != 0 {
		message := a.errorMessage(status)
		a.releaseStatus(status)
		return fmt.Errorf("%w: %s", RuntimeUnavailable, message)
	}
	return nil
}
func bindORT(symbol uintptr) (ortAPI, error) {
	var base func() unsafe.Pointer
	purego.RegisterFunc(&base, symbol)
	p := base()
	if p == nil {
		return ortAPI{}, RuntimeUnavailable
	}
	var get func(uint32) unsafe.Pointer
	purego.RegisterFunc(&get, *(*uintptr)(p))
	table := get(23)
	if table == nil {
		return ortAPI{}, RuntimeUnavailable
	}
	slots := unsafe.Slice((*uintptr)(table), 209)
	var a ortAPI
	for _, entry := range []struct {
		fn   any
		slot int
	}{{&a.errorMessage, 2}, {&a.createEnv, 3}, {&a.createSession, 7}, {&a.run, 9}, {&a.createOptions, 10}, {&a.graph, 23}, {&a.threads, 24}, {&a.createRun, 39}, {&a.terminate, 46}, {&a.tensor, 49}, {&a.data, 51}, {&a.elementType, 60}, {&a.dimensionsCount, 61}, {&a.dimensions, 62}, {&a.shape, 65}, {&a.memory, 69}, {&a.releaseEnv, 92}, {&a.releaseStatus, 93}, {&a.releaseMemory, 94}, {&a.releaseSession, 95}, {&a.releaseValue, 96}, {&a.releaseRun, 97}, {&a.releaseShape, 99}, {&a.releaseOptions, 100}, {&a.cudaAppend, 204}, {&a.cudaCreate, 205}, {&a.cudaRelease, 208}} {
		if slots[entry.slot] == 0 {
			return a, RuntimeUnavailable
		}
		purego.RegisterFunc(entry.fn, slots[entry.slot])
	}
	return a, nil
}

type onnxEngine struct {
	api                  ortAPI
	env, session, memory uintptr
	unload               func() error
	assets               Assets
}

func newONNXEngine(c Config, a Assets, device string) (Engine, error) {
	path, e := filepath.Abs(c.RuntimePath)
	if e != nil {
		return nil, RuntimeUnavailable
	}
	symbol, unload, e := loadORT(path)
	if e != nil {
		return nil, fmt.Errorf("%w: %v", RuntimeUnavailable, e)
	}
	api, e := bindORT(symbol)
	if e != nil {
		unload()
		return nil, e
	}
	engine := &onnxEngine{api: api, unload: unload, assets: a}
	ok := false
	defer func() {
		if !ok {
			engine.Close()
		}
	}()
	label := append([]byte("workshop-speech"), 0)
	if api.check(api.createEnv(3, &label[0], &engine.env)) != nil {
		return nil, RuntimeUnavailable
	}
	var opts uintptr
	if api.check(api.createOptions(&opts)) != nil {
		return nil, RuntimeUnavailable
	}
	defer api.releaseOptions(opts)
	if api.check(api.graph(opts, 99)) != nil || api.check(api.threads(opts, 4)) != nil {
		return nil, RuntimeUnavailable
	}
	if device == "cuda" {
		var cuda uintptr
		if err := api.check(api.cudaCreate(&cuda)); err != nil {
			return nil, fmt.Errorf("CUDA options: %w", err)
		}
		defer api.cudaRelease(cuda)
		if err := api.check(api.cudaAppend(opts, cuda)); err != nil {
			return nil, fmt.Errorf("CUDA provider: %w", err)
		}
	}
	model, e := filepath.Abs(filepath.Join(c.ModelPath, "model.onnx"))
	if e != nil {
		return nil, InvalidAssets
	}
	native, keep, e := ortPath(model)
	if e != nil {
		return nil, InvalidAssets
	}
	e = api.check(api.createSession(engine.env, native, opts, &engine.session))
	runtime.KeepAlive(keep)
	if e != nil {
		return nil, e
	}
	if api.check(api.memory(1, 0, &engine.memory)) != nil {
		return nil, RuntimeUnavailable
	}
	ok = true
	return engine, nil
}
func (e *onnxEngine) Infer(ctx context.Context, features []float32, frames int) ([]float32, int, int, int, error) {
	if ctx.Err() != nil {
		return nil, 0, 0, 0, ctx.Err()
	}
	a := &e.api
	var input, length, options uintptr
	dims := []int64{1, int64(e.assets.Frontend.Mels), int64(frames)}
	lens := []int64{int64(frames)}
	one := int64(1)
	if a.check(a.tensor(e.memory, unsafe.Pointer(&features[0]), uintptr(len(features)*4), &dims[0], 3, 1, &input)) != nil {
		return nil, 0, 0, 0, Failed
	}
	defer a.releaseValue(input)
	if a.check(a.tensor(e.memory, unsafe.Pointer(&lens[0]), 8, &one, 1, 7, &length)) != nil {
		return nil, 0, 0, 0, Failed
	}
	defer a.releaseValue(length)
	if a.check(a.createRun(&options)) != nil {
		return nil, 0, 0, 0, Failed
	}
	defer a.releaseRun(options)
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		select {
		case <-ctx.Done():
			_ = a.check(a.terminate(options))
		case <-done:
		}
	}()
	defer func() { close(done); wg.Wait() }()
	names := make([][]byte, 4)
	pointers := make([]*byte, 4)
	for i, s := range append(append([]string{}, e.assets.Inputs...), e.assets.Outputs...) {
		names[i] = append([]byte(s), 0)
		pointers[i] = &names[i][0]
	}
	inputs := []uintptr{input, length}
	outputs := make([]uintptr, 2)
	defer func() {
		for _, v := range outputs {
			if v != 0 {
				a.releaseValue(v)
			}
		}
	}()
	err := a.check(a.run(e.session, options, &pointers[0], &inputs[0], 2, &pointers[2], 2, &outputs[0]))
	runtime.KeepAlive(features)
	runtime.KeepAlive(lens)
	runtime.KeepAlive(names)
	if ctx.Err() != nil {
		return nil, 0, 0, 0, ctx.Err()
	}
	if err != nil {
		return nil, 0, 0, 0, Failed
	}
	shape, p, err := e.tensorData(outputs[0], 1)
	if err != nil || len(shape) != 3 || shape[0] != 1 || shape[1] < 1 || shape[1] > int64(frames) || shape[2] != int64(len(e.assets.Vocab)+1) {
		return nil, 0, 0, 0, InvalidAssets
	}
	// Official v3_ctc emits int32 lengths; older exports/fixtures use int64.
	lengthShape, lp, err := e.tensorData(outputs[1], 7)
	length64 := err == nil
	if err != nil {
		lengthShape, lp, err = e.tensorData(outputs[1], 6)
	}
	if err != nil || len(lengthShape) != 1 || lengthShape[0] != 1 {
		return nil, 0, 0, 0, InvalidAssets
	}
	var actual int64
	if length64 {
		actual = *(*int64)(lp)
	} else {
		actual = int64(*(*int32)(lp))
	}
	if actual < 0 || actual > shape[1] {
		return nil, 0, 0, 0, InvalidAssets
	}
	logits := append([]float32(nil), unsafe.Slice((*float32)(p), int(shape[1]*shape[2]))...)
	return logits, int(shape[1]), int(shape[2]), int(actual), nil
}
func (e *onnxEngine) tensorData(value uintptr, kind int32) ([]int64, unsafe.Pointer, error) {
	if value == 0 {
		return nil, nil, InvalidAssets
	}
	a := &e.api
	var info uintptr
	if a.check(a.shape(value, &info)) != nil {
		return nil, nil, InvalidAssets
	}
	defer a.releaseShape(info)
	var dtype int32
	var count uintptr
	if a.check(a.elementType(info, &dtype)) != nil || dtype != kind || a.check(a.dimensionsCount(info, &count)) != nil || count < 1 || count > 3 {
		return nil, nil, InvalidAssets
	}
	dims := make([]int64, int(count))
	if a.check(a.dimensions(info, &dims[0], count)) != nil {
		return nil, nil, InvalidAssets
	}
	var p unsafe.Pointer
	if a.check(a.data(value, &p)) != nil || p == nil {
		return nil, nil, InvalidAssets
	}
	return dims, p, nil
}
func (e *onnxEngine) Close() error {
	if e.session != 0 {
		e.api.releaseSession(e.session)
		e.session = 0
	}
	if e.memory != 0 {
		e.api.releaseMemory(e.memory)
		e.memory = 0
	}
	if e.env != 0 {
		e.api.releaseEnv(e.env)
		e.env = 0
	}
	if e.unload != nil {
		f := e.unload
		e.unload = nil
		return f()
	}
	return nil
}
