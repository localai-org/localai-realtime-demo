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

Start the LocalAI backend (downloads the four sub-models on first run):

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
