package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mudler/minimal-realtime-assistant/realtime"
)

func TestSetupToolsDefaultRegistersWeather(t *testing.T) {
	reg := realtime.NewRegistry()
	cleanup, err := setupTools(context.Background(), "", reg)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if _, ok := reg.Get("get_weather"); !ok {
		t.Fatal("expected get_weather to be registered by default")
	}
}

func TestSetupToolsBadConfigFailsFast(t *testing.T) {
	p := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(p, []byte(`{not json`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := setupTools(context.Background(), p, realtime.NewRegistry()); err == nil {
		t.Fatal("expected error for invalid mcp config")
	}
}

func TestSetupToolsMissingBinaryFailsFast(t *testing.T) {
	p := filepath.Join(t.TempDir(), "mcp.json")
	if err := os.WriteFile(p, []byte(`{"mcpServers":{"x":{"command":"definitely-not-a-real-binary-xyz-12345"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := setupTools(context.Background(), p, realtime.NewRegistry()); err == nil {
		t.Fatal("expected error connecting to missing binary")
	}
}
