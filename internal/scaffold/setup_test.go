package scaffold

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakePrompter scripts select indices in order.
type fakePrompter struct {
	selects []int
	i       int
}

func (f *fakePrompter) Select(_ string, _ []string, _ int) (int, error) {
	v := f.selects[f.i]
	f.i++
	return v, nil
}

func TestRunAssumeYes(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "docker-compose.yml")
	pipe := filepath.Join(dir, "gpt-realtime.yaml")
	if err := os.WriteFile(pipe, []byte(samplePipeline), 0o644); err != nil {
		t.Fatal(err)
	}

	var w bytes.Buffer
	choice, err := Run(context.Background(), nil, &w, Options{OutPath: out, PipelinePath: pipe, AssumeYes: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if choice.LLM != "gemma-4-e2b-it-qat-q4_0" || choice.TTS != "kokoro-multi-lang-v1.0-sherpa" {
		t.Errorf("defaults not applied: %+v", choice)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("compose not written: %v", err)
	}
	if !strings.Contains(string(b), "- gemma-4-e2b-it-qat-q4_0\n") {
		t.Error("compose missing chosen llm in command list")
	}
	p, _ := os.ReadFile(pipe)
	if !strings.Contains(string(p), "  llm: gemma-4-e2b-it-qat-q4_0\n") {
		t.Error("pipeline not patched")
	}
}

func TestRunInteractivePicks(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "compose.yml")
	// selects: [0] hardware index (amd-rocm = index 3), [1] LLM index 1 (lfm2.5),
	// [2] TTS index 2 (qwen3-tts-cpp).
	fp := &fakePrompter{selects: []int{3, 1, 2}}

	choice, err := Run(context.Background(), fp, new(bytes.Buffer), Options{OutPath: out, OutExplicit: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if choice.Variant.Name != "amd-rocm" {
		t.Errorf("variant = %q, want amd-rocm", choice.Variant.Name)
	}
	if choice.LLM != "lfm2.5-8b-a1b" {
		t.Errorf("llm = %q", choice.LLM)
	}
	if choice.TTS != "qwen3-tts-cpp" {
		t.Errorf("tts = %q", choice.TTS)
	}
}

// The tracked default compose is overwritten freely, with a .bak saved first.
func TestRunBacksUpDefaultCompose(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "docker-compose.yml")
	if err := os.WriteFile(out, []byte("OLD CONTENT"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), nil, new(bytes.Buffer), Options{OutPath: out, AssumeYes: true}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	bak, err := os.ReadFile(out + ".bak")
	if err != nil || string(bak) != "OLD CONTENT" {
		t.Errorf(".bak should hold the old content, got %q err %v", bak, err)
	}
	if b, _ := os.ReadFile(out); !strings.Contains(string(b), "services:") {
		t.Error("compose should have been overwritten with fresh content")
	}
}

// An explicit --out target that exists is refused unless --force.
func TestRunRefusesExplicitOutWithoutForce(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "custom.yml")
	if err := os.WriteFile(out, []byte("existing"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), nil, new(bytes.Buffer), Options{OutPath: out, OutExplicit: true, AssumeYes: true}); err == nil {
		t.Error("expected refusal to overwrite an explicit --out without --force")
	}
	if _, err := Run(context.Background(), nil, new(bytes.Buffer), Options{OutPath: out, OutExplicit: true, Force: true, AssumeYes: true}); err != nil {
		t.Errorf("--force should overwrite: %v", err)
	}
}
