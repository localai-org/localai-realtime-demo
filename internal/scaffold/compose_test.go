package scaffold

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "regenerate golden files")

func variantByName(t *testing.T, name string) Variant {
	t.Helper()
	for _, v := range Variants() {
		if v.Name == name {
			return v
		}
	}
	t.Fatalf("no variant %q", name)
	return Variant{}
}

func TestRenderComposeGolden(t *testing.T) {
	cases := []struct {
		variant string
		llm     string
		tts     string
		golden  string
	}{
		{"cpu", "gemma-4-e2b-it-qat-q4_0", "vits-piper-it_IT-paola-sherpa", "compose-cpu.golden.yml"},
		{"nvidia-cuda-12", "lfm2.5-8b-a1b", "qwen3-tts-cpp", "compose-nvidia-cuda-12.golden.yml"},
		{"nvidia-cuda-13", "gemma-4-e2b-it-qat-q4_0", "kokoros", "compose-nvidia-cuda-13.golden.yml"},
		{"amd-rocm", "gemma-4-e2b-it-qat-q4_0", "kokoro-multi-lang-v1.0-sherpa", "compose-amd-rocm.golden.yml"},
		{"intel", "gemma-4-e2b-it-qat-q4_0", "vits-piper-it_IT-paola-sherpa", "compose-intel.golden.yml"},
		{"vulkan", "gemma-4-e2b-it-qat-q4_0", "kokoros", "compose-vulkan.golden.yml"},
		{"metal", "gemma-4-e2b-it-qat-q4_0", "vits-piper-it_IT-paola-sherpa", "compose-metal.golden.yml"},
		{"jetson", "lfm2.5-8b-a1b", "vits-piper-it_IT-paola-sherpa", "compose-jetson.golden.yml"},
	}
	for _, tc := range cases {
		t.Run(tc.variant, func(t *testing.T) {
			got, err := RenderCompose(Choice{Variant: variantByName(t, tc.variant), LLM: tc.llm, TTS: tc.tts})
			if err != nil {
				t.Fatalf("RenderCompose: %v", err)
			}
			path := filepath.Join("testdata", tc.golden)
			if *update {
				if err := os.WriteFile(path, got, 0o644); err != nil {
					t.Fatalf("write golden: %v", err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read golden (run with -update first): %v", err)
			}
			if string(got) != string(want) {
				t.Errorf("render mismatch for %s:\n--- got ---\n%s", tc.variant, got)
			}
		})
	}
}

// TestRenderComposeContent asserts the load-bearing pieces independent of the
// golden bytes: the right image tag, the GPU stanza (present only for GPU
// variants, with the spec's nvidia.com/gpu driver), and the baked command list
// in VAD, STT, LLM, TTS order.
func TestRenderComposeContent(t *testing.T) {
	t.Run("cpu has no device stanza", func(t *testing.T) {
		out := mustRender(t, "cpu", "gemma-4-e2b-it-qat-q4_0", "vits-piper-it_IT-paola-sherpa")
		if !strings.Contains(out, "image: localai/localai:master\n") {
			t.Error("CPU image tag missing")
		}
		if strings.Contains(out, "deploy:") || strings.Contains(out, "/dev/dri") || strings.Contains(out, "/dev/kfd") {
			t.Error("CPU variant must not emit a GPU device stanza")
		}
		assertCommandOrder(t, out, "gemma-4-e2b-it-qat-q4_0", "vits-piper-it_IT-paola-sherpa")
	})

	t.Run("nvidia cuda-12 image + reservation stanza", func(t *testing.T) {
		out := mustRender(t, "nvidia-cuda-12", "lfm2.5-8b-a1b", "qwen3-tts-cpp")
		if !strings.Contains(out, "image: localai/localai:master-gpu-nvidia-cuda-12\n") {
			t.Error("NVIDIA cuda-12 image tag missing")
		}
		for _, frag := range []string{"deploy:", "driver: nvidia.com/gpu", "capabilities: [gpu, utility]"} {
			if !strings.Contains(out, frag) {
				t.Errorf("NVIDIA stanza missing %q", frag)
			}
		}
		assertCommandOrder(t, out, "lfm2.5-8b-a1b", "qwen3-tts-cpp")
	})

	t.Run("nvidia cuda-13 reuses the nvidia stanza", func(t *testing.T) {
		out := mustRender(t, "nvidia-cuda-13", "gemma-4-e2b-it-qat-q4_0", "kokoros")
		if !strings.Contains(out, "image: localai/localai:master-gpu-nvidia-cuda-13\n") {
			t.Error("NVIDIA cuda-13 image tag missing")
		}
		if !strings.Contains(out, "driver: nvidia.com/gpu") {
			t.Error("cuda-13 should reuse the nvidia stanza")
		}
	})

	t.Run("amd image + device nodes + group_add", func(t *testing.T) {
		out := mustRender(t, "amd-rocm", "gemma-4-e2b-it-qat-q4_0", "kokoro-multi-lang-v1.0-sherpa")
		if !strings.Contains(out, "image: localai/localai:master-gpu-hipblas\n") {
			t.Error("AMD image tag missing")
		}
		for _, frag := range []string{"/dev/kfd", "/dev/dri", "- render", "- video"} {
			if !strings.Contains(out, frag) {
				t.Errorf("AMD stanza missing %q", frag)
			}
		}
	})

	t.Run("intel and vulkan expose dri only", func(t *testing.T) {
		for _, name := range []string{"intel", "vulkan"} {
			out := mustRender(t, name, "gemma-4-e2b-it-qat-q4_0", "kokoros")
			if !strings.Contains(out, "/dev/dri") || strings.Contains(out, "/dev/kfd") || strings.Contains(out, "deploy:") {
				t.Errorf("%s should expose /dev/dri only", name)
			}
		}
		if !strings.Contains(mustRender(t, "vulkan", "gemma-4-e2b-it-qat-q4_0", "kokoros"), "image: localai/localai:master-gpu-vulkan\n") {
			t.Error("vulkan image tag missing")
		}
	})

	t.Run("metal has image but no stanza", func(t *testing.T) {
		out := mustRender(t, "metal", "gemma-4-e2b-it-qat-q4_0", "vits-piper-it_IT-paola-sherpa")
		if !strings.Contains(out, "image: localai/localai:master-metal-darwin-arm64\n") {
			t.Error("metal image tag missing")
		}
		if strings.Contains(out, "/dev/") || strings.Contains(out, "deploy:") {
			t.Error("metal must not emit a device stanza")
		}
	})

	t.Run("jetson image + nvidia stanza", func(t *testing.T) {
		out := mustRender(t, "jetson", "lfm2.5-8b-a1b", "vits-piper-it_IT-paola-sherpa")
		if !strings.Contains(out, "image: localai/localai:master-nvidia-l4t-arm64\n") {
			t.Error("jetson image tag missing")
		}
		if !strings.Contains(out, "driver: nvidia.com/gpu") {
			t.Error("jetson should use the nvidia stanza")
		}
	})

	t.Run("warms the pipeline models via LOAD_TO_MEMORY", func(t *testing.T) {
		out := mustRender(t, "cpu", "gemma-4-e2b-it-qat-q4_0", "vits-piper-it_IT-paola-sherpa")
		want := "      - LOAD_TO_MEMORY=" + VADModel + "," + TranscriptionModel + ",gemma-4-e2b-it-qat-q4_0,vits-piper-it_IT-paola-sherpa\n"
		if !strings.Contains(out, want) {
			t.Errorf("LOAD_TO_MEMORY warmup line wrong; want:\n%s\ngot:\n%s", want, out)
		}
	})
}

func TestRenderComposeDeterministic(t *testing.T) {
	c := Choice{Variant: variantByName(t, "nvidia-cuda-12"), LLM: "lfm2.5-8b-a1b", TTS: "qwen3-tts-cpp"}
	a, _ := RenderCompose(c)
	b, _ := RenderCompose(c)
	if string(a) != string(b) {
		t.Error("RenderCompose is not deterministic")
	}
}

func mustRender(t *testing.T, variant, llm, tts string) string {
	t.Helper()
	out, err := RenderCompose(Choice{Variant: variantByName(t, variant), LLM: llm, TTS: tts})
	if err != nil {
		t.Fatalf("RenderCompose: %v", err)
	}
	return string(out)
}

// assertCommandOrder checks the gallery-install command list is exactly
// VAD, transcription, chosen LLM, chosen TTS — in that order.
func assertCommandOrder(t *testing.T, out, llm, tts string) {
	t.Helper()
	want := "    command:\n      - " + VADModel + "\n      - " + TranscriptionModel + "\n      - " + llm + "\n      - " + tts + "\n"
	if !strings.Contains(out, want) {
		t.Errorf("command list/order wrong; want:\n%s\ngot:\n%s", want, out)
	}
}
