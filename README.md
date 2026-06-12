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

| Flag            | Env                      | Default                            |
|-----------------|--------------------------|------------------------------------|
| `-ws-url`       | `OPENAI_WS_BASE_URL`     | `ws://localhost:8080/v1/realtime`  |
| `-api-key`      | `OPENAI_API_KEY`         | `sk-xxx` (LocalAI ignores it)      |
| `-model`        | `ASSISTANT_MODEL`        | `gpt-4o-realtime-preview`          |
| `-voice`        | `ASSISTANT_VOICE`        | server default                     |
| `-language`     | `ASSISTANT_LANGUAGE`     | auto-detect (ISO-639-1, e.g. `it`) |
| `-instructions` | `ASSISTANT_INSTRUCTIONS` | short helpful-assistant prompt     |
| `-sample-rate`  | —                        | `24000`                            |

## Adding a tool

Implement `realtime.Tool` (see `tools/weather.go`) and `registry.Register(...)`
it in `cmd/assistant/main.go`.

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
