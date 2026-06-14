// Package mcp bridges Model Context Protocol servers into the assistant: it
// starts the configured MCP servers, discovers their tools, and exposes each as
// a realtime.Tool so the voice model can call them.
package mcp

import (
	"encoding/json"
	"fmt"
	"os"
)

// ServerSpec is one stdio MCP server: the command to run and how. It matches the
// standard mcpServers JSON entry (the shape published by github.com/mudler/mcps).
type ServerSpec struct {
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
}

// Config is a parsed mcpServers JSON file.
type Config struct {
	MCPServers map[string]ServerSpec `json:"mcpServers"`
}

// LoadConfig reads and validates an mcpServers JSON file. It fails fast: an
// unreadable file, invalid JSON, zero servers, or a server with no command are
// all errors, so a misconfiguration never silently degrades into "no tools".
func LoadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read mcp config: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse mcp config %s: %w", path, err)
	}
	if len(cfg.MCPServers) == 0 {
		return Config{}, fmt.Errorf(`mcp config %s: no servers defined under "mcpServers"`, path)
	}
	for name, s := range cfg.MCPServers {
		if s.Command == "" {
			return Config{}, fmt.Errorf("mcp config %s: server %q has no command", path, name)
		}
	}
	return cfg, nil
}
