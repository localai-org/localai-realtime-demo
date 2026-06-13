package scaffold

import "testing"

func TestImageForFallsBackToCPU(t *testing.T) {
	if got := ImageFor(Hardware{Accel: CPU}).Image; got != "localai/localai:master" {
		t.Errorf("CPU image = %q", got)
	}
	if got := ImageFor(Hardware{Accel: NvidiaCUDA13}).Image; got != "localai/localai:master-gpu-nvidia-cuda-13" {
		t.Errorf("cuda-13 image = %q", got)
	}
	if got := ImageFor(Hardware{Accel: AMDROCm}).Image; got != "localai/localai:master-gpu-hipblas" {
		t.Errorf("AMD image = %q", got)
	}
	// An out-of-range accelerator must still yield a runnable (CPU) variant.
	if got := ImageFor(Hardware{Accel: Accelerator(99)}); got.Accel != CPU {
		t.Errorf("unknown accel should fall back to CPU, got %d", got.Accel)
	}
}

// All curated TTS voices are annotated streaming (the spec's UX shows them so),
// derived from the streamingBackends constant.
func TestPreferredTTSAllStreaming(t *testing.T) {
	for _, m := range PreferredTTS() {
		if !m.Streaming() {
			t.Errorf("TTS %s (backend %s) should be streaming per the constant", m.ID, m.Backend)
		}
	}
}

func TestStreamingBackendsConstant(t *testing.T) {
	if streamingBackends["sherpa-onnx"] != true || streamingBackends["qwen3-tts-cpp"] != true {
		t.Error("sherpa-onnx and qwen3-tts-cpp must be streaming backends")
	}
	if streamingBackends["piper"] {
		t.Error("piper is not a streaming backend")
	}
}

func TestExactlyOneDefaultPerTable(t *testing.T) {
	for name, tbl := range map[string][]Model{"llm": PreferredLLMs(), "tts": PreferredTTS()} {
		n := 0
		for _, m := range tbl {
			if m.Default {
				n++
			}
		}
		if n != 1 {
			t.Errorf("%s table has %d defaults, want exactly 1", name, n)
		}
	}
}
