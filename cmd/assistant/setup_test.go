package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runSetup with --yes is the non-interactive path: it must produce a
// docker-compose.yml and patch the pipeline config without prompting.
func TestRunSetupYes(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "docker-compose.yml")
	pipe := filepath.Join(dir, "gpt-realtime.yaml")
	if err := os.WriteFile(pipe, []byte("name: gpt-realtime\npipeline:\n  vad: silero-vad-ggml\n  llm: old-llm\n  tts: old-tts\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := runSetup([]string{"--yes", "--out", out, "--pipeline", pipe}); err != nil {
		t.Fatalf("runSetup: %v", err)
	}

	compose, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("compose not written: %v", err)
	}
	if !strings.Contains(string(compose), "services:") || !strings.Contains(string(compose), "image: localai/localai:") {
		t.Error("compose content looks wrong")
	}
	patched, _ := os.ReadFile(pipe)
	if !strings.Contains(string(patched), "  llm: gemma-4-e2b-it-qat-q4_0\n") {
		t.Errorf("pipeline not patched:\n%s", patched)
	}
}
