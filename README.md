# Minimal Realtime Assistant

A tiny Go client for [LocalAI](https://localai.io)'s realtime (WebSocket) API.
It runs a full voice conversation — you talk into the mic, the model talks back
through the speaker — and includes one example tool call (`get_weather`).

Inspired by [VoxInput](https://github.com/richiejp/VoxInput), reduced to just the
realtime conversation loop.

## Requirements

- Go 1.24+
- A C toolchain (`CGO_ENABLED=1`) — the audio layer uses
  [malgo](https://github.com/gen2brain/malgo). On Linux install ALSA headers,
  e.g. `sudo apt install libasound2-dev`.
- A running LocalAI instance serving a realtime-capable model. The included
  [`docker-compose.yml`](./docker-compose.yml) brings one up for you (see
  [Quick start](#quick-start-docker-compose) below); [`localai/`](./localai/)
  documents the realtime pipeline config (VAD + STT + LLM + TTS) and how to
  deploy it on your own instance.

## Quick start (Docker Compose)

Start the LocalAI backend (downloads the sub-models on first run):

```bash
docker compose up
```

Then build and run the client against it — no extra flags needed:

```bash
CGO_ENABLED=1 go build -o assistant ./cmd/assistant
./assistant -model gpt-realtime
```

See [`localai/README.md`](./localai/README.md) for details and how to point the
client at an existing LocalAI instance instead.

### Scaffold a hardware-appropriate stack (`assistant setup`)

The tracked `docker-compose.yml` is a CPU default. To generate one tuned to this
host's accelerator — the right LocalAI image variant, the GPU `deploy`/device
stanza, and your chosen LLM + TTS models baked into the gallery-install list —
run the scaffolder *before* bringing LocalAI up:

```bash
CGO_ENABLED=1 go build -o assistant ./cmd/assistant
./assistant setup            # interactive: detect-then-confirm HW, pick LLM + TTS
./assistant setup --yes      # non-interactive: detected HW + default models (CI/board images)
docker compose up            # first boot gallery-installs the baked models (needs network)
```

It detects the GPU best-effort (`nvidia-smi`/`rocminfo`/`lspci`/`uname`) and
preselects a default, but the menu always shows so you can override — e.g.
scaffold on a laptop for a Jetson. It needs **no running LocalAI**. The generated
compose overwrites the tracked CPU default (a `.bak` is kept first); use
`--out <file>` to write elsewhere (refused if it exists, unless `--force`).
`setup` also writes your `pipeline.llm`/`pipeline.tts` choices into
`localai/models/gpt-realtime.yaml`.

> **TTS default — Kokoro multilingual.** The default voice
> (`kokoro-multi-lang-v1.0-sherpa`) needs a `sherpa-onnx` backend that carries the
> Kokoro routing (LocalAI ≥ commit `20341087`). If your `cpu-sherpa-onnx` backend
> is older, Kokoro fails to load — pick another voice in `setup` (e.g.
> `qwen3-tts-cpp`, also multilingual), or refresh the backend
> (`POST /backends/upgrade/cpu-sherpa-onnx`) once a newer OCI is published.

## Build

```bash
CGO_ENABLED=1 go build -o assistant ./cmd/assistant
```

## Run

```bash
./assistant \
  -ws-url ws://localhost:8080/v1/realtime \
  -model gpt-realtime
```

Then just speak. Server-side VAD detects when you start and stop talking. Ask
"what's the weather in Paris?" to trigger the `get_weather` tool — the model
calls the tool, gets canned data back, and speaks the result.

## Configuration

| Flag                | Env                      | Default                            |
|---------------------|--------------------------|------------------------------------|
| `-ws-url`           | `OPENAI_WS_BASE_URL`     | `ws://localhost:8080/v1/realtime`  |
| `-api-key`          | `OPENAI_API_KEY`         | `sk-xxx` (LocalAI ignores it)      |
| `-model`            | `ASSISTANT_MODEL`        | `gpt-4o-realtime-preview`          |
| `-voice`            | `ASSISTANT_VOICE`        | server default                     |
| `-language`         | `ASSISTANT_LANGUAGE`     | auto-detect (ISO-639-1, e.g. `it`) |
| `-instructions`     | `ASSISTANT_INSTRUCTIONS` | short helpful-assistant prompt     |
| `-sample-rate`      | —                        | `24000`                            |
| `-fallback-ws-url`  | `FALLBACK_WS_BASE_URL`   | `ws://localhost:8080/v1/realtime`  |
| `-fallback-model`   | `FALLBACK_MODEL`         | (same as `-model`)                 |
| `-fallback-api-key` | `FALLBACK_API_KEY`       | (same as `-api-key`)               |

### Fallback / failover

The assistant tries the **primary** endpoint (`-ws-url`) first and automatically
falls back to a **local** endpoint (`-fallback-ws-url`, defaulting to the
docker-compose instance) when the primary can't be reached — for example when a
remote primary loses internet connectivity.

Failover happens at (re)connect time. If the connection drops mid-conversation
the assistant reconnects, walking the endpoint list from the top; conversation
context is **not** preserved across a switch. Because every reconnect starts from
the primary, the assistant automatically returns to it once it is healthy again.

A short tone plays on each switch: an ascending sweep when moving (back) to the
primary, a descending sweep when dropping to the fallback. If every endpoint is
unreachable the assistant keeps retrying with a capped backoff (1s up to 30s)
until one answers or you quit (Ctrl-C).

Each endpoint may use its own model and API key via `-fallback-model` /
`-fallback-api-key`; when omitted they reuse `-model` / `-api-key`.

## Adding a tool

Implement `realtime.Tool` (see `tools/weather.go`) and `registry.Register(...)`
it in `cmd/assistant/tools_setup.go`. To add tools without writing Go, point the
assistant at MCP servers instead — see [MCP servers (tools)](#mcp-servers-tools).

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
`get_weather` example. Startup fails fast if a server can't be reached, the
config lists no servers, or two servers expose a tool with the same name.

## Layout

- `audio/` — malgo full-duplex device (mic in, speaker out)
- `realtime/` — WebSocket client, session setup, event loop, tool registry
- `tools/` — the `get_weather` example
- `cmd/assistant/` — CLI entry point

## Echo cancellation (optional)

In a voice session the assistant's speech plays on your speakers and the open
mic re-captures it, which causes self-echo and makes the assistant interrupt
itself. The client can remove its own voice from the mic with
[LocalVQE](https://github.com/localai-org/LocalVQE) neural acoustic echo
cancellation (16 kHz, runs on the CPU, no cgo - loaded via `purego`).

`make` (or `make build`) produces a **self-contained binary** with the LocalVQE
library and the compact 1.3M model **bundled in** (via `go:embed`). AEC is then
on automatically with no flags, no env vars, and no runtime files - at startup
the embedded assets are extracted to a content-hashed cache dir and loaded.

```bash
make            # builds liblocalvqe.so, downloads the model, bundles both in
./bin/assistant # AEC on automatically
```

There are no library or model path options. To bundle a different model, rebuild:

```bash
make build LOCALVQE_MODEL_FILE=localvqe-v1.3-4.8M-f32.gguf
```

A plain `go build` (or `make build-noembed`) bundles nothing, so AEC is simply
**disabled** and the mic passes through untouched (logged once at startup). Two
knobs remain: `-aec` (force it off) and `-aec-delay-ms` (reference delay, default
50).

This removes only the assistant's own voice; genuine speech still reaches the
server's VAD, so you can still interrupt the assistant by talking (barge-in).

## Barge-in

When the server's VAD reports that you've started talking, the client
immediately **flushes its local playback buffer** so the assistant stops
mid-sentence. This matters because the server streams a whole response in a
burst — seconds of TTS sit buffered on the client after the server is "done",
so cancelling the response server-side alone would not stop the audio already
in the pipe. If a response is still being generated, the client also sends
`response.cancel`.
