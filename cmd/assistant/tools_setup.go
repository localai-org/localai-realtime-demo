package main

import (
	"context"

	"github.com/mudler/minimal-realtime-assistant/mcp"
	"github.com/mudler/minimal-realtime-assistant/realtime"
)

// setupTools populates registry with the assistant's tools and returns a cleanup
// function. When mcpConfigPath is set, it connects to the configured MCP servers
// and registers every tool they expose; cleanup closes those connections. When
// it's empty, the assistant runs with no tools and cleanup is a no-op. Any MCP
// config or connection problem is returned as an error (fail fast).
func setupTools(ctx context.Context, mcpConfigPath string, registry *realtime.Registry) (func() error, error) {
	noop := func() error { return nil }

	if mcpConfigPath == "" {
		return noop, nil
	}

	cfg, err := mcp.LoadConfig(mcpConfigPath)
	if err != nil {
		return noop, err
	}
	bridge, err := mcp.Connect(ctx, cfg)
	if err != nil {
		return noop, err
	}
	for _, t := range bridge.Tools() {
		registry.Register(t)
	}
	return bridge.Close, nil
}
