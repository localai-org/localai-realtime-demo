# MCP Server Bridge Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let users point the assistant at a set of MCP servers (standard `mcpServers` JSON, as published by `mudler/mcps`); their tools become the assistant's tools and replace the built-in `get_weather` example.

**Architecture:** A new top-level `mcp/` package parses the config, starts each MCP server as a stdio subprocess via the official Go SDK, discovers its tools, and wraps each as a `realtime.Tool`. `cmd/assistant` registers those tools instead of the weather example when a config is supplied. Tools are already entirely client-side (`realtime.Registry` → `session.update` → `handleFunctionCall`), so nothing in `realtime/` changes. Fail fast on any config/startup problem; runtime tool-call errors stay non-fatal.

**Tech Stack:** Go 1.24, `github.com/modelcontextprotocol/go-sdk/mcp` v1.3.0 (the SDK already in the module cache, used by the sibling `wiz` project), stdlib `testing`.

---

## File structure

- `mcp/config.go` — `Config`/`ServerSpec` structs + `LoadConfig` (parse + validate the `mcpServers` JSON).
- `mcp/tool.go` — `bridgedTool`, the `realtime.Tool` adapter over one MCP tool.
- `mcp/bridge.go` — `Bridge` (owns sessions + tools), internal `connect` (list/wrap/dedup over transports), public `Connect` (builds `CommandTransport`s from `Config`).
- `mcp/server_test.go` — test helpers: in-memory MCP servers used by the bridge/tool tests.
- `mcp/config_test.go`, `mcp/bridge_test.go`, `mcp/tool_test.go` — tests.
- `cmd/assistant/tools_setup.go` — `setupTools`, the registry-population seam (MCP vs weather).
- `cmd/assistant/tools_setup_test.go` — tests for `setupTools`.
- `cmd/assistant/main.go` — add the `-mcp-config` flag, call `setupTools`, genericize the default instructions (modify only).
- `go.mod` / `go.sum` — add the SDK dependency.
- `README.md` — short "MCP servers" usage section (modify only).

The SDK package is imported as `sdk` (aliased) everywhere in `mcp/` so the local package name `mcp` is never ambiguous with the SDK's `mcp`.

---

### Task 1: Config parsing

**Files:**
- Create: `mcp/config.go`
- Test: `mcp/config_test.go`
- Modify: `go.mod`, `go.sum`

- [ ] **Step 1: Add the SDK dependency**

Run:
```bash
go get github.com/modelcontextprotocol/go-sdk@v1.3.0
```
Expected: `go.mod` gains `github.com/modelcontextprotocol/go-sdk v1.3.0`; `go.sum` updated. (The version is already in the local module cache.)

- [ ] **Step 2: Write the failing tests**

Create `mcp/config_test.go`:
```go
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
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./mcp/`
Expected: FAIL — `undefined: LoadConfig` (package doesn't compile yet).

- [ ] **Step 4: Implement `config.go`**

Create `mcp/config.go`:
```go
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
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./mcp/`
Expected: PASS (all five `TestLoadConfig*`).

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum mcp/config.go mcp/config_test.go
git commit -m "feat(mcp): parse and validate mcpServers config"
```

---

### Task 2: Tool adapter + connection bridge (in-memory tested)

**Files:**
- Create: `mcp/tool.go`, `mcp/bridge.go`, `mcp/server_test.go`, `mcp/bridge_test.go`, `mcp/tool_test.go`

This task builds the core: wrapping MCP tools as `realtime.Tool`s and the internal `connect` that lists/dedupes them. It is tested entirely with in-memory MCP servers (no subprocesses). The public `Connect` (subprocess transport) is Task 3.

- [ ] **Step 1: Write the in-memory test server helper**

Create `mcp/server_test.go`:
```go
package mcp

import (
	"context"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// echoArgs is the input schema for the test echo tools.
type echoArgs struct {
	Text string `json:"text" jsonschema:"text to echo back"`
}

// echoOut is an empty structured-output type (the test tools return their result
// as explicit text Content; Out is required by the generic AddTool signature).
type echoOut struct{}

// startEchoServer runs an in-memory MCP server exposing one echo tool per name.
// Each tool returns the text "<name>:<input text>" as a single TextContent. The
// returned transport is the client side; the server stops when ctx is cancelled.
func startEchoServer(t *testing.T, ctx context.Context, toolNames ...string) sdk.Transport {
	t.Helper()
	serverT, clientT := sdk.NewInMemoryTransports()
	server := sdk.NewServer(&sdk.Implementation{Name: "echo", Version: "v1"}, nil)
	for _, n := range toolNames {
		name := n
		sdk.AddTool(server, &sdk.Tool{Name: name, Description: "echo tool " + name},
			func(ctx context.Context, req *sdk.CallToolRequest, in echoArgs) (*sdk.CallToolResult, echoOut, error) {
				return &sdk.CallToolResult{
					Content: []sdk.Content{&sdk.TextContent{Text: name + ":" + in.Text}},
				}, echoOut{}, nil
			})
	}
	go func() { _ = server.Run(ctx, serverT) }()
	return clientT
}

// startErrorServer runs an in-memory MCP server with one tool "boom" that always
// returns a tool-level error (IsError) carrying the text "boom: it failed".
func startErrorServer(t *testing.T, ctx context.Context) sdk.Transport {
	t.Helper()
	serverT, clientT := sdk.NewInMemoryTransports()
	server := sdk.NewServer(&sdk.Implementation{Name: "err", Version: "v1"}, nil)
	sdk.AddTool(server, &sdk.Tool{Name: "boom", Description: "always errors"},
		func(ctx context.Context, req *sdk.CallToolRequest, in echoArgs) (*sdk.CallToolResult, echoOut, error) {
			return &sdk.CallToolResult{
				IsError: true,
				Content: []sdk.Content{&sdk.TextContent{Text: "boom: it failed"}},
			}, echoOut{}, nil
		})
	go func() { _ = server.Run(ctx, serverT) }()
	return clientT
}
```

- [ ] **Step 2: Write the failing bridge + tool tests**

Create `mcp/bridge_test.go`:
```go
package mcp

import (
	"context"
	"strings"
	"testing"
)

func TestConnectListsAndExecutesTools(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tr := startEchoServer(t, ctx, "alpha", "beta")
	b, err := connect(ctx, []namedTransport{{name: "s1", transport: tr}})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	tools := b.Tools()
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(tools))
	}
	if tools[0].Name() != "alpha" || tools[1].Name() != "beta" {
		t.Fatalf("unexpected tool order: %q, %q", tools[0].Name(), tools[1].Name())
	}
	if tools[0].Parameters() == nil {
		t.Fatal("expected non-nil parameters schema")
	}
	out, err := tools[0].Execute(ctx, `{"text":"hi"}`)
	if err != nil {
		t.Fatal(err)
	}
	if out != "alpha:hi" {
		t.Fatalf("output = %q, want alpha:hi", out)
	}
}

func TestConnectDuplicateToolNameFailsFast(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	t1 := startEchoServer(t, ctx, "dup")
	t2 := startEchoServer(t, ctx, "dup")
	_, err := connect(ctx, []namedTransport{{"s1", t1}, {"s2", t2}})
	if err == nil {
		t.Fatal("expected duplicate tool name error")
	}
	if !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("error = %v, want it to mention duplicate", err)
	}
}

func TestConnectAcrossServersOrdersByServer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	t1 := startEchoServer(t, ctx, "alpha")
	t2 := startEchoServer(t, ctx, "beta")
	b, err := connect(ctx, []namedTransport{{"s1", t1}, {"s2", t2}})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	got := []string{b.Tools()[0].Name(), b.Tools()[1].Name()}
	if got[0] != "alpha" || got[1] != "beta" {
		t.Fatalf("tool order = %v, want [alpha beta]", got)
	}
}
```

Create `mcp/tool_test.go`:
```go
package mcp

import (
	"context"
	"strings"
	"testing"
)

func TestExecuteToolErrorBecomesGoError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tr := startErrorServer(t, ctx)
	b, err := connect(ctx, []namedTransport{{"s1", tr}})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	_, err = b.Tools()[0].Execute(ctx, `{}`)
	if err == nil {
		t.Fatal("expected error from IsError result")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Fatalf("error = %v, want it to contain boom", err)
	}
}

func TestExecuteEmptyArgs(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tr := startEchoServer(t, ctx, "alpha")
	b, err := connect(ctx, []namedTransport{{"s1", tr}})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	out, err := b.Tools()[0].Execute(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if out != "alpha:" {
		t.Fatalf("output = %q, want alpha:", out)
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./mcp/`
Expected: FAIL — `undefined: connect`, `undefined: namedTransport`, `b.Tools undefined` (bridge not written yet).

- [ ] **Step 4: Implement the tool adapter**

Create `mcp/tool.go`:
```go
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// bridgedTool adapts one MCP server tool to the realtime.Tool interface. The
// session is the live client connection the call is dispatched over.
type bridgedTool struct {
	session     *sdk.ClientSession
	name        string
	description string
	schema      any // the server's InputSchema, passed through verbatim
}

func (t *bridgedTool) Name() string       { return t.name }
func (t *bridgedTool) Description() string { return t.description }
func (t *bridgedTool) Parameters() any    { return t.schema }

// Execute forwards the model's JSON arguments to the MCP server and returns the
// joined text of the result. A tool-level error (IsError) is surfaced as a Go
// error so the realtime client reports the failure back to the model.
func (t *bridgedTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	args := map[string]any{}
	if s := strings.TrimSpace(argsJSON); s != "" {
		if err := json.Unmarshal([]byte(s), &args); err != nil {
			return "", fmt.Errorf("parse %s args: %w", t.name, err)
		}
	}
	res, err := t.session.CallTool(ctx, &sdk.CallToolParams{Name: t.name, Arguments: args})
	if err != nil {
		return "", fmt.Errorf("call %s: %w", t.name, err)
	}
	text := contentText(res.Content)
	if res.IsError {
		return "", fmt.Errorf("%s", text)
	}
	return text, nil
}

// contentText joins the text parts of an MCP tool result, newline-separated.
// Non-text content (images, audio) is ignored — the realtime tool protocol is
// text-only.
func contentText(content []sdk.Content) string {
	var parts []string
	for _, c := range content {
		if tc, ok := c.(*sdk.TextContent); ok {
			parts = append(parts, tc.Text)
		}
	}
	return strings.Join(parts, "\n")
}
```

- [ ] **Step 5: Implement the bridge**

Create `mcp/bridge.go`:
```go
package mcp

import (
	"context"
	"fmt"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mudler/minimal-realtime-assistant/realtime"
)

// Bridge owns the live MCP client sessions and the realtime tools they expose.
// Close it when the assistant shuts down to terminate the server subprocesses.
type Bridge struct {
	sessions []*sdk.ClientSession
	tools    []realtime.Tool
}

// Tools returns the realtime tools discovered across all connected servers, in a
// deterministic order (servers in the order connect received them, tools in the
// order each server listed them).
func (b *Bridge) Tools() []realtime.Tool { return b.tools }

// Close shuts every MCP session down (stopping its subprocess). It attempts all
// closes and returns the first error, if any.
func (b *Bridge) Close() error {
	var first error
	for _, s := range b.sessions {
		if err := s.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// namedTransport pairs a server name (for error messages and ordering) with the
// transport to dial it.
type namedTransport struct {
	name      string
	transport sdk.Transport
}

// connect dials each transport in order, lists its tools (paging through the
// cursor), and wraps them as realtime tools. Any failure — a dial error, a list
// error, or a tool name already seen on another server — tears down every
// session opened so far and returns an error (fail fast).
func connect(ctx context.Context, servers []namedTransport) (*Bridge, error) {
	client := sdk.NewClient(&sdk.Implementation{Name: "minimal-realtime-assistant", Version: "0.1.0"}, nil)
	b := &Bridge{}
	seen := map[string]string{} // tool name -> server it came from

	for _, srv := range servers {
		session, err := client.Connect(ctx, srv.transport, nil)
		if err != nil {
			b.Close()
			return nil, fmt.Errorf("connect mcp server %q: %w", srv.name, err)
		}
		b.sessions = append(b.sessions, session)

		cursor := ""
		for {
			res, err := session.ListTools(ctx, &sdk.ListToolsParams{Cursor: cursor})
			if err != nil {
				b.Close()
				return nil, fmt.Errorf("list tools from mcp server %q: %w", srv.name, err)
			}
			for _, tl := range res.Tools {
				if from, dup := seen[tl.Name]; dup {
					b.Close()
					return nil, fmt.Errorf("duplicate tool name %q from mcp servers %q and %q", tl.Name, from, srv.name)
				}
				seen[tl.Name] = srv.name
				b.tools = append(b.tools, &bridgedTool{
					session:     session,
					name:        tl.Name,
					description: tl.Description,
					schema:      tl.InputSchema,
				})
			}
			if res.NextCursor == "" {
				break
			}
			cursor = res.NextCursor
		}
	}
	return b, nil
}
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test ./mcp/`
Expected: PASS (config tests from Task 1 plus all bridge/tool tests). If `TestConnectListsAndExecutesTools` fails on tool order, note that the SDK lists tools alphabetically; the test uses already-sorted names so it should hold.

- [ ] **Step 7: Commit**

```bash
git add mcp/tool.go mcp/bridge.go mcp/server_test.go mcp/bridge_test.go mcp/tool_test.go
git commit -m "feat(mcp): bridge MCP server tools to realtime tools"
```

---

### Task 3: Public `Connect` over stdio subprocesses

**Files:**
- Modify: `mcp/bridge.go`
- Test: `mcp/bridge_test.go`

- [ ] **Step 1: Write the failing test**

Append to `mcp/bridge_test.go`:
```go
func TestConnectMissingBinaryFailsFast(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg := Config{MCPServers: map[string]ServerSpec{
		"x": {Command: "definitely-not-a-real-binary-xyz-12345"},
	}}
	if _, err := Connect(ctx, cfg); err == nil {
		t.Fatal("expected error connecting to a missing binary")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./mcp/ -run TestConnectMissingBinaryFailsFast`
Expected: FAIL — `undefined: Connect`.

- [ ] **Step 3: Implement the public `Connect`**

Add to `mcp/bridge.go` — update the import block and append the function. Replace the existing import block with:
```go
import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sort"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mudler/minimal-realtime-assistant/realtime"
)
```
Then append:
```go
// Connect starts every configured MCP server as a stdio subprocess, discovers
// its tools, and returns a Bridge exposing them as realtime tools. Servers are
// dialed in sorted-name order so tool ordering is deterministic across runs. Any
// failure aborts and tears down already-started servers (fail fast). Call
// Bridge.Close to stop the subprocesses.
func Connect(ctx context.Context, cfg Config) (*Bridge, error) {
	names := make([]string, 0, len(cfg.MCPServers))
	for name := range cfg.MCPServers {
		names = append(names, name)
	}
	sort.Strings(names)

	servers := make([]namedTransport, 0, len(names))
	for _, name := range names {
		spec := cfg.MCPServers[name]
		cmd := exec.CommandContext(ctx, spec.Command, spec.Args...)
		cmd.Env = os.Environ()
		for k, v := range spec.Env {
			cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
		}
		servers = append(servers, namedTransport{
			name:      name,
			transport: &sdk.CommandTransport{Command: cmd},
		})
	}
	return connect(ctx, servers)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./mcp/`
Expected: PASS (all of `mcp/`, including `TestConnectMissingBinaryFailsFast`). `CommandTransport.Connect` calls `cmd.Start()`, which fails immediately for a non-existent binary.

- [ ] **Step 5: Commit**

```bash
git add mcp/bridge.go mcp/bridge_test.go
git commit -m "feat(mcp): start MCP servers as stdio subprocesses"
```

---

### Task 4: Wire into the assistant client

**Files:**
- Create: `cmd/assistant/tools_setup.go`
- Test: `cmd/assistant/tools_setup_test.go`
- Modify: `cmd/assistant/main.go`

- [ ] **Step 1: Write the failing tests**

Create `cmd/assistant/tools_setup_test.go`:
```go
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./cmd/assistant/ -run TestSetupTools`
Expected: FAIL — `undefined: setupTools`.

- [ ] **Step 3: Implement `setupTools`**

Create `cmd/assistant/tools_setup.go`:
```go
package main

import (
	"context"
	"fmt"

	"github.com/mudler/minimal-realtime-assistant/mcp"
	"github.com/mudler/minimal-realtime-assistant/realtime"
	"github.com/mudler/minimal-realtime-assistant/tools"
)

// setupTools populates registry with the assistant's tools and returns a cleanup
// function. When mcpConfigPath is set, it connects to the configured MCP servers
// and registers their tools — the get_weather example is omitted — and cleanup
// closes those connections. When it's empty, only the built-in get_weather
// example is registered and cleanup is a no-op. Any MCP config or connection
// problem is returned as an error (fail fast).
func setupTools(ctx context.Context, mcpConfigPath string, registry *realtime.Registry) (func() error, error) {
	noop := func() error { return nil }

	if mcpConfigPath == "" {
		weather, err := tools.NewWeather()
		if err != nil {
			return noop, fmt.Errorf("init weather tool: %w", err)
		}
		registry.Register(weather)
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
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./cmd/assistant/ -run TestSetupTools`
Expected: PASS (all three).

- [ ] **Step 5: Wire `setupTools` into `main` and add the flag**

In `cmd/assistant/main.go`, add the flag next to the others (after the `instructions` flag, before `sampleRate`):
```go
	mcpConfig := flag.String("mcp-config", env("ASSISTANT_MCP_CONFIG", ""),
		"path to an mcpServers JSON file; when set, its tools replace the built-in get_weather example")
```

Replace the existing weather-registration block:
```go
	// Tools.
	registry := realtime.NewRegistry()
	weather, err := tools.NewWeather()
	if err != nil {
		log.Fatalln("init weather tool:", err)
	}
	registry.Register(weather)
```
with:
```go
	// Tools. With -mcp-config the assistant's tools come from the configured MCP
	// servers; otherwise it registers the built-in get_weather example.
	registry := realtime.NewRegistry()
	cleanupTools, err := setupTools(ctx, *mcpConfig, registry)
	if err != nil {
		log.Fatalln("tools:", err)
	}
	defer cleanupTools()
```

- [ ] **Step 6: Genericize the default instructions**

In `cmd/assistant/main.go`, change the `instructions` flag default. Replace:
```go
	instructions := flag.String("instructions", env("ASSISTANT_INSTRUCTIONS",
		"You are a helpful voice assistant. Keep replies short and conversational. Use the get_weather tool when the user asks about the weather."),
		"system instructions")
```
with:
```go
	instructions := flag.String("instructions", env("ASSISTANT_INSTRUCTIONS",
		"You are a helpful voice assistant. Keep replies short and conversational."),
		"system instructions")
```

- [ ] **Step 7: Verify `tools` is still imported**

`cmd/assistant/main.go` no longer references `tools` directly (it moved to `tools_setup.go`). Remove the now-unused `"github.com/mudler/minimal-realtime-assistant/tools"` import line from `main.go` if present.

Run: `go build ./...`
Expected: builds clean. If it fails with `"...tools" imported and not used`, remove that import line from `main.go`; if it fails with `undefined: tools`, the import is still needed — leave it.

- [ ] **Step 8: Run the full test suite**

Run: `go test ./...`
Expected: PASS across all packages. Existing `realtime`, `scaffold`, and `tools` tests are unchanged.

- [ ] **Step 9: Commit**

```bash
git add cmd/assistant/main.go cmd/assistant/tools_setup.go cmd/assistant/tools_setup_test.go
git commit -m "feat: register MCP server tools via -mcp-config, else get_weather"
```

---

### Task 5: Document the feature

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Find where tools/usage are described**

Run: `grep -n "get_weather\|tool\|Usage\|## " README.md`
Expected: shows the README's section headers and any mention of the weather tool, so the new section lands near the existing tools/usage prose.

- [ ] **Step 2: Add an "MCP servers" section**

Add this section to `README.md` under the usage/tools area (adjust the surrounding heading level to match the file):
```markdown
## MCP servers (tools)

By default the assistant registers a single mocked `get_weather` tool to
demonstrate function calling. To give it real tools, point it at a set of
[Model Context Protocol](https://modelcontextprotocol.io/) servers using the
standard `mcpServers` JSON format (the same format published by
[`mudler/mcps`](https://github.com/mudler/mcps)):

```json
{
  "mcpServers": {
    "weather": {
      "command": "docker",
      "args": ["run", "-i", "--rm", "ghcr.io/mudler/mcps/weather:master"],
      "env": { "API_KEY": "..." }
    }
  }
}
```

Run the assistant with the config:

```bash
./assistant -model gpt-realtime -mcp-config mcp.json
# or: ASSISTANT_MCP_CONFIG=mcp.json ./assistant -model gpt-realtime
```

When `-mcp-config` is set, the assistant connects to every listed server at
startup, registers all of their tools, and does **not** register the
`get_weather` example. Startup fails fast if a server can't be reached, a server
lists no usable tools, or two servers expose a tool with the same name.
```

- [ ] **Step 3: Verify the docs build/render**

Run: `grep -n "mcp-config" README.md`
Expected: the new section is present.

- [ ] **Step 4: Commit**

```bash
git add README.md
git commit -m "docs: document MCP server configuration"
```

---

## Self-review notes

- **Spec coverage:** config schema + `LoadConfig` (Task 1); `Bridge`/`connect`/`bridgedTool` with fail-fast dedup and IsError handling (Task 2); stdio `Connect` + fail-fast (Task 3); `-mcp-config`/`ASSISTANT_MCP_CONFIG` flag, weather-vs-MCP selection, generic default instructions (Task 4); docs (Task 5). The spec's non-goals (no setup-wizard changes, no remote transports) are respected — no task touches `internal/scaffold` or adds HTTP transports.
- **Type consistency:** `Config`/`ServerSpec`, `Bridge.{Tools,Close}`, `namedTransport{name,transport}`, `connect`, `Connect`, `bridgedTool{session,name,description,schema}`, `contentText`, and `setupTools` signatures are identical everywhere they appear.
- **Fail-fast paths** all funnel through returned errors; only `cmd/assistant/main.go` turns them into `log.Fatalln`, keeping the `mcp` package free of process exits and fully testable.
