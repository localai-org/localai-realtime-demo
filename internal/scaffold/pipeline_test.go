package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A pipeline config with a streaming block + comments + reasoning_effort, to
// prove PatchPipelineYAML rewrites only the top-level llm/tts and preserves
// everything else (the spec requires the streaming block to survive).
const samplePipeline = `name: gpt-realtime
pipeline:
  vad: silero-vad-ggml
  transcription: parakeet-cpp-tdt-0.6b-v3
  # fast laptop default
  llm: gemma-4-e2b-it-qat-q4_0
  # piper voice
  tts: voice-it-paola-medium
  streaming:
    llm: true
    tts: true
    clause_chunking: true
  reasoning_effort: none
`

func TestPatchPipelineYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gpt-realtime.yaml")
	if err := os.WriteFile(path, []byte(samplePipeline), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := PatchPipelineYAML(path, "lfm2.5-8b-a1b", "qwen3-tts-cpp"); err != nil {
		t.Fatalf("PatchPipelineYAML: %v", err)
	}
	out, _ := os.ReadFile(path)
	s := string(out)

	if !strings.Contains(s, "\n  llm: lfm2.5-8b-a1b\n") {
		t.Errorf("pipeline.llm not updated:\n%s", s)
	}
	if !strings.Contains(s, "\n  tts: qwen3-tts-cpp\n") {
		t.Errorf("pipeline.tts not updated:\n%s", s)
	}
	if strings.Contains(s, "gemma-4-e2b-it-qat-q4_0") || strings.Contains(s, "voice-it-paola-medium") {
		t.Errorf("old model ids should be gone:\n%s", s)
	}
	// Streaming block + comments + reasoning_effort preserved verbatim.
	for _, frag := range []string{
		"  streaming:\n    llm: true\n    tts: true\n    clause_chunking: true\n",
		"# fast laptop default",
		"# piper voice",
		"  reasoning_effort: none\n",
		"  vad: silero-vad-ggml\n",
	} {
		if !strings.Contains(s, frag) {
			t.Errorf("expected preserved fragment %q in:\n%s", frag, s)
		}
	}
}

func TestPatchPipelineYAMLPartial(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "p.yaml")
	if err := os.WriteFile(path, []byte(samplePipeline), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := PatchPipelineYAML(path, "lfm2.5-8b-a1b", ""); err != nil {
		t.Fatalf("PatchPipelineYAML: %v", err)
	}
	out, _ := os.ReadFile(path)
	if !strings.Contains(string(out), "  llm: lfm2.5-8b-a1b\n") {
		t.Error("llm should be patched")
	}
	if !strings.Contains(string(out), "  tts: voice-it-paola-medium\n") {
		t.Error("tts should be untouched when empty")
	}
}

func TestPatchPipelineYAMLMissingKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "p.yaml")
	if err := os.WriteFile(path, []byte("name: x\npipeline:\n  vad: silero-vad-ggml\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := PatchPipelineYAML(path, "a", "b"); err == nil {
		t.Error("expected error when pipeline.llm/tts keys are absent")
	}
}

func TestPatchPipelineYAMLMissingFile(t *testing.T) {
	if err := PatchPipelineYAML(filepath.Join(t.TempDir(), "nope.yaml"), "a", "b"); err == nil {
		t.Error("expected error for missing file")
	}
}
