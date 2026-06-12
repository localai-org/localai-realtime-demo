package localvqe

import (
	"fmt"
	"unsafe"

	"github.com/ebitengine/purego"
)

type LocalVQE struct {
	lib uintptr
	ctx uintptr

	fnFree            func(uintptr)
	fnProcessS16      func(uintptr, uintptr, uintptr, int32, uintptr) int32
	fnProcessFrameS16 func(uintptr, uintptr, uintptr, int32, uintptr) int32
	fnReset           func(uintptr)
	fnLastError       func(uintptr) string
	fnSampleRate      func(uintptr) int32
	fnHopLength       func(uintptr) int32
	fnSetNoiseGate    func(uintptr, int32, float32) int32
}

// New loads the shared library and model.
func New(libPath, modelPath string) (*LocalVQE, error) {
	lib, err := purego.Dlopen(libPath, purego.RTLD_LAZY)
	if err != nil {
		return nil, fmt.Errorf("dlopen %s: %w", libPath, err)
	}

	d := &LocalVQE{lib: lib}

	var fnNew func(uintptr) uintptr
	purego.RegisterLibFunc(&fnNew, lib, "localvqe_new")
	purego.RegisterLibFunc(&d.fnFree, lib, "localvqe_free")
	purego.RegisterLibFunc(&d.fnProcessS16, lib, "localvqe_process_s16")
	purego.RegisterLibFunc(&d.fnProcessFrameS16, lib, "localvqe_process_frame_s16")
	purego.RegisterLibFunc(&d.fnReset, lib, "localvqe_reset")
	purego.RegisterLibFunc(&d.fnLastError, lib, "localvqe_last_error")
	purego.RegisterLibFunc(&d.fnSampleRate, lib, "localvqe_sample_rate")
	purego.RegisterLibFunc(&d.fnHopLength, lib, "localvqe_hop_length")
	purego.RegisterLibFunc(&d.fnSetNoiseGate, lib, "localvqe_set_noise_gate")

	pathBytes := append([]byte(modelPath), 0) // null-terminated
	d.ctx = fnNew(uintptr(unsafe.Pointer(&pathBytes[0])))
	if d.ctx == 0 {
		purego.Dlclose(lib)
		return nil, fmt.Errorf("localvqe_new failed for %s", modelPath)
	}

	return d, nil
}

// ProcessFrameS16Into is the zero-alloc hot path: mic, ref, and out must each
// have exactly HopLength() samples.
func (d *LocalVQE) ProcessFrameS16Into(mic, ref, out []int16) error {
	if len(mic) != len(ref) || len(mic) != len(out) {
		return fmt.Errorf("localvqe: mic/ref/out length mismatch: %d/%d/%d", len(mic), len(ref), len(out))
	}
	if len(mic) == 0 {
		return nil
	}
	ret := d.fnProcessFrameS16(
		d.ctx,
		uintptr(unsafe.Pointer(&mic[0])),
		uintptr(unsafe.Pointer(&ref[0])),
		int32(len(mic)),
		uintptr(unsafe.Pointer(&out[0])),
	)
	if ret != 0 {
		return fmt.Errorf("localvqe_process_frame_s16 error %d: %s", ret, d.LastError())
	}
	return nil
}

// Reset clears streaming state (overlap buffers, GRU hidden state).
func (d *LocalVQE) Reset() { d.fnReset(d.ctx) }

// SetNoiseGate enables/disables the residual-echo noise gate. When enabled, any
// hop whose RMS is at or below thresholdDBFS is replaced with zeros.
func (d *LocalVQE) SetNoiseGate(enabled bool, thresholdDBFS float32) error {
	var en int32
	if enabled {
		en = 1
	}
	ret := d.fnSetNoiseGate(d.ctx, en, thresholdDBFS)
	if ret != 0 {
		return fmt.Errorf("localvqe_set_noise_gate error %d: %s", ret, d.LastError())
	}
	return nil
}

// HopLength returns the model hop length in samples.
func (d *LocalVQE) HopLength() int { return int(d.fnHopLength(d.ctx)) }

// SampleRate returns the model sample rate (16000).
func (d *LocalVQE) SampleRate() int { return int(d.fnSampleRate(d.ctx)) }

func (d *LocalVQE) LastError() string {
	return d.fnLastError(d.ctx)
}

// Close frees the context and unloads the library.
func (d *LocalVQE) Close() {
	if d.ctx != 0 {
		d.fnFree(d.ctx)
		d.ctx = 0
	}
	if d.lib != 0 {
		purego.Dlclose(d.lib)
		d.lib = 0
	}
}
