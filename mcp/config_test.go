package mcp

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "mcp.json")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadConfigValid(t *testing.T) {
	p := writeTemp(t, `{"mcpServers":{"weather":{"command":"docker","args":["run","-i","--rm","img"],"env":{"K":"V"}}}}`)
	cfg, err := LoadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	s, ok := cfg.MCPServers["weather"]
	if !ok {
		t.Fatal("expected weather server")
	}
	if s.Command != "docker" {
		t.Fatalf("command = %q", s.Command)
	}
	if len(s.Args) != 4 || s.Args[0] != "run" {
		t.Fatalf("args = %v", s.Args)
	}
	if s.Env["K"] != "V" {
		t.Fatalf("env = %v", s.Env)
	}
}

func TestLoadConfigInvalidJSON(t *testing.T) {
	if _, err := LoadConfig(writeTemp(t, `{not json`)); err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestLoadConfigEmptyServers(t *testing.T) {
	if _, err := LoadConfig(writeTemp(t, `{"mcpServers":{}}`)); err == nil {
		t.Fatal("expected error for zero servers")
	}
}

func TestLoadConfigMissingCommand(t *testing.T) {
	if _, err := LoadConfig(writeTemp(t, `{"mcpServers":{"x":{"args":["a"]}}}`)); err == nil {
		t.Fatal("expected error for missing command")
	}
}

func TestLoadConfigUnreadable(t *testing.T) {
	if _, err := LoadConfig(filepath.Join(t.TempDir(), "nope.json")); err == nil {
		t.Fatal("expected error for missing file")
	}
}
