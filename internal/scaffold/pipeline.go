package scaffold

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// pipelineLLMRe and pipelineTTSRe match the llm:/tts: keys that are *direct*
// children of `pipeline:` — exactly two leading spaces. Any nested streaming
// block's llm:/tts: are indented deeper, so these patterns deliberately don't
// match them: that's how PatchPipelineYAML rewires the pipeline stages while
// leaving everything else (streaming block, reasoning_effort, comments) intact.
var (
	pipelineLLMRe = regexp.MustCompile(`^(  llm:)[^\n]*$`)
	pipelineTTSRe = regexp.MustCompile(`^(  tts:)[^\n]*$`)
)

// PatchPipelineYAML rewrites the pipeline.llm and pipeline.tts values in the
// realtime pipeline config at path, leaving everything else byte-for-byte
// unchanged — comments, the streaming block (which degrades gracefully for
// non-streaming backends), reasoning_effort, key order. It's a targeted line
// edit rather than a YAML re-encode precisely so the file's hand-written
// comments and structure survive.
//
// Empty llm/tts means "leave that stage alone". It errors if path can't be read
// or if a stage was requested but its key isn't present in the file.
func PatchPipelineYAML(path, llm, tts string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("patch pipeline: read %s: %w", path, err)
	}
	lines := strings.Split(string(b), "\n")

	llmDone := llm == ""
	ttsDone := tts == ""
	for i, line := range lines {
		if !llmDone && pipelineLLMRe.MatchString(line) {
			lines[i] = "  llm: " + llm
			llmDone = true
			continue
		}
		if !ttsDone && pipelineTTSRe.MatchString(line) {
			lines[i] = "  tts: " + tts
			ttsDone = true
		}
	}
	if !llmDone {
		return fmt.Errorf("patch pipeline: no top-level pipeline.llm key in %s", path)
	}
	if !ttsDone {
		return fmt.Errorf("patch pipeline: no top-level pipeline.tts key in %s", path)
	}

	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		return fmt.Errorf("patch pipeline: write %s: %w", path, err)
	}
	return nil
}
