package scaffold

import (
	"context"
	"fmt"
	"io"
	"os"
)

// Prompter is the thin seam between the setup wizard and the terminal, so the
// flow can be driven by a scripted fake in tests instead of real stdin/stdout.
// All three steps (confirm hardware, pick LLM, pick TTS) go through it.
type Prompter interface {
	// Select presents labelled options and returns the chosen index; def is the
	// pre-selected (detected/default) option.
	Select(label string, options []string, def int) (int, error)
}

// Options configures a setup run. The scaffolder never talks to a running
// LocalAI.
type Options struct {
	OutPath      string // compose file to write (default docker-compose.yml)
	OutExplicit  bool   // true when the user passed --out (changes clobber policy)
	PipelinePath string // realtime pipeline config to patch (empty = skip)
	AssumeYes    bool   // accept the detected hardware + default models, no prompts
	Force        bool   // overwrite an explicit --out target that already exists
}

const showAllLabel = "[ show all from the LocalAI gallery ]"

// Run executes the setup wizard: detect-then-confirm hardware, pick a chat LLM,
// pick a TTS voice, then render docker-compose.yml and patch the pipeline
// config. With AssumeYes it takes every default and never calls the Prompter, so
// it doubles as the non-interactive (--yes) path. Progress is written to w.
func Run(ctx context.Context, p Prompter, w io.Writer, opts Options) (Choice, error) {
	variant, err := chooseVariant(p, w, opts.AssumeYes)
	if err != nil {
		return Choice{}, err
	}
	llm, err := chooseModel(ctx, p, w, opts, "LLM", "llm", PreferredLLMs())
	if err != nil {
		return Choice{}, err
	}
	tts, err := chooseModel(ctx, p, w, opts, "TTS", "tts", PreferredTTS())
	if err != nil {
		return Choice{}, err
	}

	choice := Choice{Variant: variant, LLM: llm, TTS: tts}
	if err := writeCompose(choice, opts, w); err != nil {
		return choice, err
	}
	if opts.PipelinePath != "" {
		if err := PatchPipelineYAML(opts.PipelinePath, llm, tts); err != nil {
			return choice, err
		}
		fmt.Fprintf(w, "Updated %s   (pipeline.llm=%s, pipeline.tts=%s)\n", opts.PipelinePath, llm, tts)
	}
	fmt.Fprintf(w, "\nNext:  docker compose up      # first boot installs the baked models\n")
	return choice, nil
}

// chooseVariant detects the host accelerator (detect-then-confirm) and always
// shows the menu so the user can override — e.g. when scaffolding for another
// board. The detected variant is pre-selected.
func chooseVariant(p Prompter, w io.Writer, assumeYes bool) (Variant, error) {
	hw := DetectHardware()
	detected := ImageFor(hw)
	fmt.Fprintf(w, "Detecting hardware… %s\n", hw.Detail)
	if assumeYes {
		return detected, nil
	}
	vs := Variants()
	labels := make([]string, len(vs))
	def := 0
	for i, v := range vs {
		labels[i] = fmt.Sprintf("%s -> %s", v.Label, v.Image)
		if v.Accel == detected.Accel {
			def = i
			if hw.Detected {
				labels[i] += "   (detected)"
			}
		}
	}
	i, err := p.Select("Target hardware?", labels, def)
	if err != nil {
		return Variant{}, err
	}
	return vs[i], nil
}

// chooseModel presents the curated shortlist for a stage and returns the chosen
// model id. The last option opens the full gallery (filtered by usecase tag).
func chooseModel(ctx context.Context, p Prompter, w io.Writer, opts Options, stage, usecase string, curated []Model) (string, error) {
	def := defaultIndex(curated)
	if opts.AssumeYes {
		return curated[def].ID, nil
	}
	labels := make([]string, 0, len(curated)+1)
	for _, m := range curated {
		labels = append(labels, modelLabel(m))
	}
	labels = append(labels, showAllLabel)

	i, err := p.Select(stage+":", labels, def)
	if err != nil {
		return "", err
	}
	if i < len(curated) {
		return curated[i].ID, nil
	}
	return chooseFromGallery(ctx, p, w, usecase)
}

// chooseFromGallery fetches the gallery and lets the user pick; a fetch error
// (e.g. offline) is surfaced so Run aborts with a clear message rather than
// silently — the curated set is the offline-friendly path.
func chooseFromGallery(ctx context.Context, p Prompter, w io.Writer, usecase string) (string, error) {
	fmt.Fprintf(w, "Fetching %s models from the gallery…\n", usecase)
	models, err := FetchGalleryModels(ctx, usecase)
	if err != nil {
		return "", fmt.Errorf("%w (show all needs network; the preferred set works offline)", err)
	}
	if len(models) == 0 {
		return "", fmt.Errorf("gallery: no %s models found", usecase)
	}
	labels := make([]string, len(models))
	for i, m := range models {
		labels[i] = m.Name
	}
	i, err := p.Select("Choose from gallery", labels, 0)
	if err != nil {
		return "", err
	}
	return models[i].Name, nil
}

// writeCompose renders and writes the compose file. The tracked default
// (docker-compose.yml) is overwritten freely — a .bak is saved first; an
// explicit --out target that already exists is refused unless --force.
func writeCompose(c Choice, opts Options, w io.Writer) error {
	if _, err := os.Stat(opts.OutPath); err == nil {
		if opts.OutExplicit && !opts.Force {
			return fmt.Errorf("%s already exists; pass --force to overwrite", opts.OutPath)
		}
		if err := backup(opts.OutPath); err != nil {
			return err
		}
		fmt.Fprintf(w, "Backed up existing %s -> %s.bak\n", opts.OutPath, opts.OutPath)
	}
	out, err := RenderCompose(c)
	if err != nil {
		return err
	}
	if err := os.WriteFile(opts.OutPath, out, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", opts.OutPath, err)
	}
	fmt.Fprintf(w, "Wrote %s      (image %s%s, command: [%s, %s, %s, %s])\n",
		opts.OutPath, c.Variant.Image, gpuNote(c.Variant), VADModel, TranscriptionModel, c.LLM, c.TTS)
	return nil
}

func backup(path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("back up %s: %w", path, err)
	}
	if err := os.WriteFile(path+".bak", b, 0o644); err != nil {
		return fmt.Errorf("back up %s: %w", path, err)
	}
	return nil
}

func gpuNote(v Variant) string {
	if v.DeviceStanza != "" {
		return ", GPU stanza"
	}
	return ""
}

func defaultIndex(models []Model) int {
	for i, m := range models {
		if m.Default {
			return i
		}
	}
	return 0
}

func modelLabel(m Model) string {
	label := m.ID + "    " + m.Description
	if m.Streaming() {
		label += " (streaming)"
	}
	return label
}
