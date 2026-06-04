# Minimal Realtime Assistant — Design

**Date:** 2026-06-04
**Status:** Approved

## Goal

A small Go client that connects to LocalAI's realtime WebSocket API and runs a
full end-to-end voice conversation: the user speaks, the model speaks back, and
the model can call a tool mid-conversation. Inspired by
[VoxInput](https://github.com/richiejp/VoxInput), but stripped of everything
VoxInput needs for desktop control.

### Explicitly out of scope (removed from VoxInput)

- Linux/macOS input simulation (dotool, CoreGraphics) — no keyboard/mouse output
- Screenshot tool
- Acoustic echo cancellation / LocalVQE neural noise suppression
- Bubbletea TUI
- IPC unix-socket server and the `record`/`write`/`toggle`/`status` signal IPC
- HTTP transcription-only mode (`--no-realtime`)

## Dependencies (the irreducible core)

- `github.com/WqyJh/go-openai-realtime/v2` — realtime WebSocket client. Pinned to
  the fork via `replace => github.com/richiejp/go-openai-realtime/v2` so the API
  matches VoxInput exactly. Pure Go (uses `coder/websocket`), no C compiler.
- `github.com/gen2brain/malgo` — miniaudio CGo bindings for microphone capture and
  speaker playback. **Requires `CGO_ENABLED=1` and a C toolchain.**
- `github.com/sashabaranov/go-openai/jsonschema` — generate the tool JSON schema
  from a Go struct.

## Structure (library + cmd example)

```
minimal-realtime-assistant/
├── go.mod
├── README.md                     # how to point at LocalAI + run
├── realtime/
│   ├── client.go                 # connect, session update, send audio, read-loop dispatch
│   └── tool.go                   # Tool interface + Registry (name→handler, schema)
├── audio/
│   └── duplex.go                 # malgo: mic→chan PCM16, chan PCM16→speaker
├── tools/
│   └── weather.go                # get_weather(location) mock — the 1 example
└── cmd/assistant/
    └── main.go                   # wire config + audio + client + tools, run
```

## Components

### `audio.Duplex(ctx, micOut chan<- []byte, playIn <-chan []byte) error`

A single malgo full-duplex device, PCM16 mono @ 24000 Hz.

- Capture callback copies mic frames and pushes them to `micOut` (non-blocking;
  drop + log on a full channel).
- Playback callback drains an internal byte buffer/ring that is fed from `playIn`.
  Underrun → output silence.
- Returns on context cancellation or device error.

### `realtime.Client`

Wraps the openairt client and connection.

- `Connect(ctx)` — dial the WS URL with the API key, wait for `session.created`.
- `UpdateSession(ctx)` — send a `SessionUpdateEvent` with: instructions,
  server-VAD turn detection, input PCM format + sample rate, output voice + PCM
  format + sample rate, and the registered tools.
- `SendAudio(ctx, []byte)` — base64-encode and emit `InputAudioBufferAppendEvent`.
- `Run(ctx)` — the read-loop dispatcher (see Event loop).

### `realtime.Tool` / `realtime.Registry`

```go
type Tool interface {
    Name() string
    Description() string
    ParamsSchema() jsonschema.Definition   // generated from a Go struct
    Execute(ctx context.Context, argsJSON string) (output string, err error)
}
```

`Registry` holds tools by name, builds the `[]openairt.ToolUnion` for the session
update, and routes `response.function_call_arguments.done` events to the right
tool.

### Event loop (`Client.Run`)

Reads messages and dispatches by `ServerEventType`:

- speech started / stopped → log
- `conversation.item.input_audio_transcription.completed` → print "you said: …"
- `response.created` → log "generating response"
- `response.output_audio.delta` → base64-decode → send to `playIn`
- `response.function_call_arguments.done` → registry lookup → `Execute` →
  send `ConversationItemCreate{FunctionCallOutput}` → send `ResponseCreate`
  (so the model speaks the tool result)
- `error` → log the server error message
- transient read error → log + continue; `PermanentError` → cancel + exit

## Data flow

```
mic → malgo capture → micOut chan → SendAudio (base64) → LocalAI
LocalAI → response.output_audio.delta → playIn chan → malgo playback → speaker
LocalAI → response.function_call_arguments.done → get_weather → FunctionCallOutput → ResponseCreate → model speaks the answer
```

Server VAD handles turn detection, so there is no push-to-talk — the user just
speaks.

## The example tool: `get_weather`

```go
type WeatherParams struct {
    Location string `json:"location" jsonschema_description:"City and state/country"`
}
```

`Execute` returns canned JSON (e.g. `{"location":"...","temp_c":21,"summary":"Sunny"}`).
Self-contained, no network, deterministic — purely to demonstrate the
function-calling round trip end to end.

## Configuration (flags with env fallback)

| Setting       | Flag             | Env                        | Default                          |
|---------------|------------------|----------------------------|----------------------------------|
| WS URL        | `-ws-url`        | `OPENAI_WS_BASE_URL`       | `ws://localhost:8080/v1/realtime`|
| API key       | `-api-key`       | `OPENAI_API_KEY`           | `sk-xxx` (LocalAI ignores it)    |
| Model         | `-model`         | `ASSISTANT_MODEL`          | `gpt-4o-realtime-preview`        |
| Voice         | `-voice`         | `ASSISTANT_VOICE`          | (empty = server default)         |
| Instructions  | `-instructions`  | `ASSISTANT_INSTRUCTIONS`   | a short helpful-assistant prompt |
| Sample rate   | `-sample-rate`   | —                          | `24000`                          |

## Error handling

- `openairt.PermanentError` on read → cancel the root context and exit cleanly.
- Transient read errors → log and continue the loop.
- Dropped audio chunks (channel full) → logged, not fatal.
- Audio device error → propagated up, cancels the session.

## Testing

- **Unit:** `realtime.Registry` / `tools.get_weather` — schema generation and the
  `Execute(argsJSON) → output` round trip. This is the only piece testable without
  a live server.
- **Manual / integration:** the audio + WebSocket paths are verified against a
  running LocalAI instance, per the README run instructions.
