package localvqe

import (
	"os"
	"testing"
)

// Loading the binding requires the real liblocalvqe.so + a GGUF model. Skip
// unless both are provided so `go test ./...` is green without the native lib.
func TestNewLoadsAndReportsModelGeometry(t *testing.T) {
	lib := os.Getenv("LOCALVQE_LIB")
	model := os.Getenv("LOCALVQE_MODEL")
	if lib == "" || model == "" {
		t.Skip("set LOCALVQE_LIB and LOCALVQE_MODEL to run this test")
	}
	e, err := New(lib, model)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer e.Close()
	if e.SampleRate() != 16000 {
		t.Fatalf("SampleRate = %d, want 16000", e.SampleRate())
	}
	if e.HopLength() <= 0 {
		t.Fatalf("HopLength = %d, want > 0", e.HopLength())
	}

	// One hop of silence through the identity-shaped API should not error.
	hop := e.HopLength()
	mic := make([]int16, hop)
	ref := make([]int16, hop)
	out := make([]int16, hop)
	if err := e.ProcessFrameS16Into(mic, ref, out); err != nil {
		t.Fatalf("ProcessFrameS16Into: %v", err)
	}
}
