# MCP server bridge — design

**Date:** 2026-06-14
**Status:** Approved

## Problem

The assistant ships a single mocked `get_weather` tool, registered
unconditionally in `cmd/assistant/main.go`. Users have no way to give the voice
assistant real tools. We want users to point the assistant at a set of MCP
servers — using the standard `mcpServers` JSON format (as published by
[`mudler/mcps`](https://github.com/mudler/mcps)) — and have those servers' tools
become the assistant's tools. When MCP servers are configured, the built-in
`get_weather` example is not registered.

## Goal

- Users supply a JSON file in the standard `mcpServers` shape; the assistant
  connects to each server at startup, discovers its tools, and exposes them to
  the realtime model.
- When (and only when) at least one MCP server is configured, the example
  `get_weather` tool is omitted.
- Fail fast: any misconfigured or unreachable MCP server aborts startup with a
  clear error, rather than coming up degraded.

## Non-goals

- No changes to `assistant setup` / docker-compose scaffolding. This is a
  runtime-only feature of the client.
- No remote (HTTP/SSE) MCP transports. `mudler/mcps` entries are `docker run -i`
  stdio servers, which the stdio `CommandTransport` covers. Remote transports
  can be a later addition.
- No per-tool allow/deny policy or approval gate — every tool a configured
  server advertises is exposed.

## Where it plugs in

Tools are already entirely client-side:

- `cmd/assistant/main.go` builds a `realtime.Registry` and registers tools.
- `realtime.Client.updateSession` advertises the registry via `session.update`
  (`realtime/client.go:140`, `Tools: c.registry.ToolUnions()`).
- When the model emits a function call, `Client.handleFunctionCall`
  (`realtime/client.go:231`) looks the tool up in the registry and calls
  `tool.Execute`, then returns the output to the model.

MCP therefore plugs in at registry-build time only. The `realtime` package and
the `realtime.Tool` interface are unchanged:

```go
type Tool interface {
    Name() string
    Description() string
    Parameters() any
    Execute(ctx context.Context, argsJSON string) (string, error)
}
```

Each MCP tool is wrapped as a `realtime.Tool`. The `Registry` already dedupes by
name and preserves registration order, so nothing there changes either.

## Architecture

A new top-level package `mcp/` owns config parsing, the connection lifecycle,
and the per-tool bridge. It depends on:

- `github.com/modelcontextprotocol/go-sdk/mcp` v1.3.0 (the same SDK `wiz` uses),
- the local `realtime` package (for the `Tool` interface it produces).

The package is named `mcp` and imports the SDK's `mcp` package; Go permits this
(the local package name is not an identifier within its own files), and `wiz`
does the same.

### `mcp/config.go` — schema + loader

```go
type ServerSpec struct {
    Command string            `json:"command"`
    Args    []string          `json:"args"`
    Env     map[string]string `json:"env"`
}

type Config struct {
    MCPServers map[string]ServerSpec `json:"mcpServers"`
}

func LoadConfig(path string) (Config, error)
```

`LoadConfig` reads the file and JSON-unmarshals it. It returns an error when:

- the file cannot be read,
- the JSON is invalid,
- the result has zero servers (`len(MCPServers) == 0`) — an MCP config that
  yields no servers is a misconfiguration and must fail fast rather than
  silently fall back to the weather example.

A server entry with an empty `command` is also an error (caught at connect
time at the latest; validated in `LoadConfig` for a clearer message).

This shape matches `mudler/mcps` snippets verbatim, e.g.:

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

### `mcp/bridge.go` — connection lifecycle

```go
type Bridge struct {
    sessions []*mcp.ClientSession
    tools    []realtime.Tool
}

func Connect(ctx context.Context, cfg Config) (*Bridge, error)
func (b *Bridge) Tools() []realtime.Tool
func (b *Bridge) Close() error
```

`Connect`:

1. For each server (iterated in sorted name order for deterministic tool
   ordering), build a stdio transport:
   `&mcp.CommandTransport{Command: cmd}` where `cmd = exec.CommandContext(ctx,
   spec.Command, spec.Args...)` and `cmd.Env = append(os.Environ(),
   "<k>=<v>"...)` from `spec.Env`.
2. Delegate to an unexported `connect(ctx, map[name]mcp.Transport)` that does the
   list/wrap/dedup. This split is the testability seam: tests pass
   `mcp.NewInMemoryTransports()` instead of real subprocesses.

`connect`:

1. `client := mcp.NewClient(&mcp.Implementation{Name: "minimal-realtime-assistant", Version: ...}, nil)`.
2. For each transport: `session, err := client.Connect(ctx, transport, nil)`.
   On error, tear down all already-opened sessions and return the error
   (fail fast).
3. `session.ListTools(ctx, params)`, looping while `result.NextCursor != ""` to
   page through all tools.
4. For each tool, build a `bridgedTool` (below). If a tool name has already been
   registered (by this or an earlier server), tear down and return a
   "duplicate tool name" error (fail fast — consistent with the overall policy).
5. Accumulate sessions and tools into the `Bridge`.

`Close` closes every `ClientSession`, which terminates the stdio subprocesses.
Returns the first close error (if any), after attempting all.

### `mcp/tool.go` — per-tool bridge

```go
type bridgedTool struct {
    session     *mcp.ClientSession
    name        string
    description string
    schema      any // the server's InputSchema, verbatim
}
```

- `Name()` / `Description()` come from the MCP `Tool`.
- `Parameters()` returns `schema` (the server's `Tool.InputSchema`, already a
  JSON-schema `map[string]any` from the client side). It flows unchanged into
  `Registry.ToolUnions` → the `session.update` tool definition.
- `Execute(ctx, argsJSON)`:
  1. Unmarshal `argsJSON` into `map[string]any` (empty/`""` → empty map).
  2. `result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})`.
     A transport error is returned as-is.
  3. Concatenate the `TextContent` parts of `result.Content` (newline-joined).
  4. If `result.IsError`, return the joined text as a Go `error`. The existing
     `handleFunctionCall` already formats a tool error into the model output
     (`"error: %v"`), so the model is told the call failed.

## Wiring in `cmd/assistant/main.go`

- Add flag `mcpConfig := flag.String("mcp-config", env("ASSISTANT_MCP_CONFIG", ""), "path to an mcpServers JSON file; when set, its tools replace the example get_weather tool")`.
- Change the default `-instructions` to a generic string with no tool-specific
  sentence: `"You are a helpful voice assistant. Keep replies short and conversational."`
  (The weather example still works because the tool's own description guides the
  model; the default simply stops hard-coding a tool that may be absent.)
- Build the registry:

  ```go
  registry := realtime.NewRegistry()
  if *mcpConfig != "" {
      cfg, err := mcp.LoadConfig(*mcpConfig)
      if err != nil { log.Fatalln("mcp config:", err) }
      bridge, err := mcp.Connect(ctx, cfg)
      if err != nil { log.Fatalln("mcp connect:", err) } // fail fast
      defer bridge.Close()
      for _, t := range bridge.Tools() {
          registry.Register(t)
      }
  } else {
      weather, err := tools.NewWeather()
      if err != nil { log.Fatalln("init weather tool:", err) }
      registry.Register(weather)
  }
  ```

Everything downstream (client construction, connect, run loop) is unchanged.

## Failure behavior (fail fast)

| Condition                                   | Result                          |
|---------------------------------------------|---------------------------------|
| `-mcp-config` unset                         | weather example registered      |
| config file unreadable / invalid JSON       | `log.Fatalln`, exit             |
| config has zero servers                     | `log.Fatalln`, exit             |
| any server fails to connect / list tools    | `log.Fatalln`, exit; all opened sessions closed |
| duplicate tool name across servers          | `log.Fatalln`, exit; sessions closed |
| a tool call fails at runtime (`IsError`)    | error returned to the model, session continues |

Runtime tool failures are not fatal — only startup/config problems are. A single
bad call should not kill the voice session.

## Testing

- `mcp/config_test.go`: valid parse (command/args/env round-trip), invalid JSON
  error, empty-servers error, empty-command error.
- `mcp/bridge_test.go`: an in-memory MCP server (`mcp.NewInMemoryTransports()`
  with a real `mcp.Server` exposing two tools) drives `connect`; assert
  `Tools()` names/order/schemas, fail-fast on a duplicate tool name across two
  in-memory servers, fail-fast when a server's connect/list errors, and that
  `Close` shuts sessions down.
- `mcp/tool_test.go`: `Execute` round-trip — arguments reach the server, text
  content is joined, and `IsError` results surface as a Go error.
- No changes to existing `realtime`, `scaffold`, or `tools` tests. The weather
  tool and its test remain (still the default, no-MCP path).

## Dependencies

Add `github.com/modelcontextprotocol/go-sdk v1.3.0` to `go.mod` (the version
already in the module cache and used by `wiz`).
