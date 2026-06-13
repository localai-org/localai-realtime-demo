package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/mudler/minimal-realtime-assistant/internal/scaffold"
)

// runSetup is the `assistant setup` subcommand: a pre-boot wizard that scaffolds
// a hardware-appropriate docker-compose.yml and patches the realtime pipeline
// config, without needing a running LocalAI. Interactive by default; --yes takes
// the detected hardware and default models.
func runSetup(args []string) error {
	fs := flag.NewFlagSet("assistant setup", flag.ContinueOnError)
	out := fs.String("out", "docker-compose.yml", "compose file to write (default overwrites the tracked one, keeping a .bak)")
	pipeline := fs.String("pipeline", "localai/models/gpt-realtime.yaml", "realtime pipeline config to patch with the chosen llm/tts (empty to skip)")
	yes := fs.Bool("yes", false, "non-interactive: accept the detected hardware and default models")
	force := fs.Bool("force", false, "overwrite an explicit --out target that already exists")
	if err := fs.Parse(args); err != nil {
		return err
	}

	// Whether --out was explicitly passed changes the clobber policy (the
	// default compose is overwritten freely; a custom target is protected).
	outExplicit := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "out" {
			outExplicit = true
		}
	})

	_, err := scaffold.Run(context.Background(), newStdinPrompter(), os.Stdout, scaffold.Options{
		OutPath:      *out,
		OutExplicit:  outExplicit,
		PipelinePath: *pipeline,
		AssumeYes:    *yes,
		Force:        *force,
	})
	return err
}

// stdinPrompter is the terminal implementation of scaffold.Prompter — the only
// part of setup that touches real stdin/stdout, kept deliberately thin so the
// flow itself stays testable with a fake.
type stdinPrompter struct {
	in *bufio.Reader
}

func newStdinPrompter() *stdinPrompter { return &stdinPrompter{in: bufio.NewReader(os.Stdin)} }

func (p *stdinPrompter) Select(label string, options []string, def int) (int, error) {
	fmt.Printf("\n%s\n", label)
	for i, o := range options {
		marker := "  "
		if i == def {
			marker = "> "
		}
		fmt.Printf("  %s%d) %s\n", marker, i+1, o)
	}
	fmt.Printf("Choice [%d]: ", def+1)
	line, err := p.in.ReadString('\n')
	if err != nil {
		return 0, err
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return def, nil
	}
	n, err := strconv.Atoi(line)
	if err != nil || n < 1 || n > len(options) {
		return 0, fmt.Errorf("invalid choice %q", line)
	}
	return n - 1, nil
}
