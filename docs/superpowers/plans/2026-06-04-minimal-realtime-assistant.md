# Minimal Realtime Assistant Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A small Go client that connects to LocalAI's realtime WebSocket API and runs a full voice conversation (mic in, speaker out) with one working tool-calling example.

**Architecture:** Three focused packages — `audio` (malgo full-duplex device), `realtime` (WS client + event loop + tool registry), `tools` (the `get_weather` example) — wired together by `cmd/assistant/main.go`. Server-side VAD handles turn-taking, so the user just talks.

**Tech Stack:** Go 1.24, `github.com/WqyJh/go-openai-realtime/v2` (pinned to the `richiejp` fork via `replace`), `github.com/gen2brain/malgo` (CGo miniaudio bindings), `github.com/sashabaranov/go-openai/jsonschema`.

**Build note:** Requires `CGO_ENABLED=1` and a C toolchain (malgo). On Linux, ALSA/Pulse dev headers must be present.

---

## File structure

```
minimal-realtime-assistant/
├── go.mod
├── README.md
├── realtime/
│   ├── tool.go          # Tool interface + Registry
│   ├── tool_test.go
│   └── client.go        # connect, session, send audio, event loop
├── audio/
│   └── duplex.go        # malgo mic capture + speaker playback
├── tools/
│   ├── weather.go       # get_weather(location) mock
│   └── weather_test.go
└── cmd/assistant/
    └── main.go          # flags/env + wiring
```

---

## Task 1: Module scaffold

**Files:**
- Create: `go.mod`

- [ ] **Step 1: Create `go.mod`**

```
module github.com/mudler/minimal-realtime-assistant

go 1.24.2

require (
	github.com/WqyJh/go-openai-realtime/v2 v2.0.0-rc.0.20260120095754-b1a91a348dbd
	github.com/gen2brain/malgo v0.11.25
	github.com/sashabaranov/go-openai v1.41.2
)

replace github.com/WqyJh/go-openai-realtime/v2 => github.com/richiejp/go-openai-realtime/v2 v2.0.0-20260213113003-1b6db572709e
```

- [ ] **Step 2: Commit**

```bash
git add go.mod
git commit -m "chore: go module scaffold with realtime + malgo deps"
```

---

## Task 2: Tool interface + Registry (TDD)

**Files:**
- Create: `realtime/tool.go`
- Test: `realtime/tool_test.go`

- [ ] **Step 1: Write the failing test**

`realtime/tool_test.go`:

```go
package realtime

import (
	"context"
	"testing"
)

type fakeTool struct{}

func (fakeTool) Name() string        { return "do_thing" }
func (fakeTool) Description() string  { return "does a thing" }
func (fakeTool) Parameters() any      { return map[string]any{"type": "object"} }
func (fakeTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	return "done:" + argsJSON, nil
}

func TestRegistryRegisterAndGet(t *testing.T) {
	r := NewRegistry()
	r.Register(fakeTool{})

	got, ok := r.Get("do_thing")
	if !ok {
		t.Fatal("expected do_thing to be registered")
	}
	out, err := got.Execute(context.Background(), `{"x":1}`)
	if err != nil {
		t.Fatal(err)
	}
	if out != `done:{"x":1}` {
		t.Fatalf("unexpected output: %q", out)
	}

	if _, ok := r.Get("missing"); ok {
		t.Fatal("expected missing tool to be absent")
	}
}

func TestRegistryToolUnions(t *testing.T) {
	r := NewRegistry()
	r.Register(fakeTool{})

	unions := r.ToolUnions()
	if len(unions) != 1 {
		t.Fatalf("expected 1 union, got %d", len(unions))
	}
	if unions[0].Function == nil || unions[0].Function.Name != "do_thing" {
		t.Fatalf("unexpected union: %+v", unions[0])
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./realtime/ -run TestRegistry -v`
Expected: FAIL — `undefined: NewRegistry` (build error).

- [ ] **Step 3: Write minimal implementation**

`realtime/tool.go`:

```go
package realtime

import (
	"context"

	openairt "github.com/WqyJh/go-openai-realtime/v2"
)

// Tool is a function the assistant can call during a conversation.
type Tool interface {
	Name() string
	Description() string
	// Parameters returns the JSON schema for the tool arguments, typically
	// produced by jsonschema.GenerateSchemaForType.
	Parameters() any
	// Execute runs the tool with the raw JSON arguments and returns the
	// output string that is sent back to the model.
	Execute(ctx context.Context, argsJSON string) (string, error)
}

// Registry holds the tools available to the assistant.
type Registry struct {
	tools map[string]Tool
	order []string
}

func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]Tool)}
}

func (r *Registry) Register(t Tool) {
	if _, exists := r.tools[t.Name()]; !exists {
		r.order = append(r.order, t.Name())
	}
	r.tools[t.Name()] = t
}

func (r *Registry) Get(name string) (Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

// ToolUnions builds the tool definitions for the realtime session update.
func (r *Registry) ToolUnions() []openairt.ToolUnion {
	unions := make([]openairt.ToolUnion, 0, len(r.order))
	for _, name := range r.order {
		t := r.tools[name]
		unions = append(unions, openairt.ToolUnion{
			Function: &openairt.ToolFunction{
				Name:        t.Name(),
				Description: t.Description(),
				Parameters:  t.Parameters(),
			},
		})
	}
	return unions
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./realtime/ -run TestRegistry -v`
Expected: PASS (both tests). Run `go mod tidy` first if the openairt import is not yet downloaded.

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum realtime/tool.go realtime/tool_test.go
git commit -m "feat: tool interface and registry"
```

---

## Task 3: get_weather example tool (TDD)

**Files:**
- Create: `tools/weather.go`
- Test: `tools/weather_test.go`

- [ ] **Step 1: Write the failing test**

`tools/weather_test.go`:

```go
package tools

import (
	"context"
	"strings"
	"testing"
)

func TestWeatherMetadata(t *testing.T) {
	w, err := NewWeather()
	if err != nil {
		t.Fatal(err)
	}
	if w.Name() != "get_weather" {
		t.Fatalf("name = %q", w.Name())
	}
	if w.Parameters() == nil {
		t.Fatal("expected non-nil parameters schema")
	}
}

func TestWeatherExecute(t *testing.T) {
	w, _ := NewWeather()
	out, err := w.Execute(context.Background(), `{"location":"Paris, France"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Paris, France") {
		t.Fatalf("expected location echoed, got %q", out)
	}
	if !strings.Contains(out, "temperature_c") {
		t.Fatalf("expected temperature_c, got %q", out)
	}
}

func TestWeatherExecuteBadJSON(t *testing.T) {
	w, _ := NewWeather()
	if _, err := w.Execute(context.Background(), `not json`); err == nil {
		t.Fatal("expected error on invalid JSON")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./tools/ -v`
Expected: FAIL — `undefined: NewWeather`.

- [ ] **Step 3: Write minimal implementation**

`tools/weather.go`:

```go
package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/sashabaranov/go-openai/jsonschema"
)

type weatherParams struct {
	Location string `json:"location" jsonschema_description:"City and optional state/country, e.g. 'Paris, France'"`
}

// Weather is a mock get_weather tool that returns canned data. It exists to
// demonstrate the function-calling round trip end to end.
type Weather struct {
	schema any
}

func NewWeather() (*Weather, error) {
	schema, err := jsonschema.GenerateSchemaForType(weatherParams{})
	if err != nil {
		return nil, fmt.Errorf("generate weather schema: %w", err)
	}
	return &Weather{schema: schema}, nil
}

func (w *Weather) Name() string        { return "get_weather" }
func (w *Weather) Description() string  { return "Get the current weather for a given location." }
func (w *Weather) Parameters() any      { return w.schema }

func (w *Weather) Execute(ctx context.Context, argsJSON string) (string, error) {
	var p weatherParams
	if err := json.Unmarshal([]byte(argsJSON), &p); err != nil {
		return "", fmt.Errorf("parse get_weather args: %w", err)
	}
	if p.Location == "" {
		p.Location = "unknown"
	}
	out, err := json.Marshal(map[string]any{
		"location":      p.Location,
		"temperature_c": 21,
		"conditions":    "Sunny",
	})
	if err != nil {
		return "", err
	}
	return string(out), nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./tools/ -v`
Expected: PASS (all three).

- [ ] **Step 5: Commit**

```bash
git add tools/weather.go tools/weather_test.go
git commit -m "feat: get_weather example tool"
```

---

## Task 4: Audio full-duplex device

**Files:**
- Create: `audio/duplex.go`

No unit test — malgo opens a real device; this is verified by build + the manual e2e in Task 7.

- [ ] **Step 1: Write the implementation**

`audio/duplex.go`:

```go
package audio

import (
	"context"
	"fmt"
	"sync"

	"github.com/gen2brain/malgo"
)

// Duplex opens the default capture+playback device as PCM16 mono at the given
// sample rate. Captured microphone frames are pushed to micOut (dropped if the
// channel is full); PCM bytes received on playIn are played through the
// speaker. It blocks until ctx is cancelled or the device errors.
func Duplex(ctx context.Context, sampleRate int, micOut chan<- []byte, playIn <-chan []byte) error {
	malgoCtx, err := malgo.InitContext(nil, malgo.ContextConfig{}, func(string) {})
	if err != nil {
		return fmt.Errorf("init audio context: %w", err)
	}
	defer func() {
		_ = malgoCtx.Uninit()
		malgoCtx.Free()
	}()

	var mu sync.Mutex
	var playBuf []byte

	// Accumulate incoming playback PCM into playBuf.
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case b := <-playIn:
				mu.Lock()
				playBuf = append(playBuf, b...)
				mu.Unlock()
			}
		}
	}()

	onData := func(out, in []byte, frames uint32) {
		// Playback: fill out from playBuf; pad with silence on underrun.
		mu.Lock()
		n := copy(out, playBuf)
		playBuf = playBuf[n:]
		if len(playBuf) == 0 {
			playBuf = nil
		}
		mu.Unlock()
		for i := n; i < len(out); i++ {
			out[i] = 0
		}

		// Capture: copy mic frames and hand off without blocking.
		chunk := make([]byte, len(in))
		copy(chunk, in)
		select {
		case micOut <- chunk:
		default:
		}
	}

	cfg := malgo.DefaultDeviceConfig(malgo.Duplex)
	cfg.Capture.Format = malgo.FormatS16
	cfg.Capture.Channels = 1
	cfg.Playback.Format = malgo.FormatS16
	cfg.Playback.Channels = 1
	cfg.SampleRate = uint32(sampleRate)
	cfg.Alsa.NoMMap = 1

	device, err := malgo.InitDevice(malgoCtx.Context, cfg, malgo.DeviceCallbacks{Data: onData})
	if err != nil {
		return fmt.Errorf("init device: %w", err)
	}
	defer device.Uninit()

	if err := device.Start(); err != nil {
		return fmt.Errorf("start device: %w", err)
	}

	<-ctx.Done()
	return nil
}
```

- [ ] **Step 2: Verify it builds**

Run: `go build ./audio/`
Expected: no output, exit 0. (If it fails with missing ALSA headers, install `libasound2-dev` / equivalent.)

- [ ] **Step 3: Commit**

```bash
git add audio/duplex.go
git commit -m "feat: malgo full-duplex audio device"
```

---

## Task 5: Realtime client + event loop

**Files:**
- Create: `realtime/client.go`

No unit test — requires a live server; verified by build + Task 7.

- [ ] **Step 1: Write the implementation**

`realtime/client.go`:

```go
package realtime

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	openairt "github.com/WqyJh/go-openai-realtime/v2"
)

type Config struct {
	WSURL        string
	HTTPURL      string
	APIKey       string
	Model        string
	Voice        string
	Instructions string
	SampleRate   int
	Timeout      time.Duration
}

// Client wraps the realtime WebSocket connection and dispatches server events.
type Client struct {
	cfg      Config
	registry *Registry
	playIn   chan<- []byte
	rt       *openairt.Client
	conn     *openairt.Conn
}

func NewClient(cfg Config, registry *Registry, playIn chan<- []byte) *Client {
	rtConf := openairt.DefaultConfig(cfg.APIKey)
	rtConf.BaseURL = cfg.WSURL
	if cfg.HTTPURL != "" {
		rtConf.APIBaseURL = cfg.HTTPURL
	}
	rtConf.HTTPClient = &http.Client{Timeout: cfg.Timeout}
	return &Client{
		cfg:      cfg,
		registry: registry,
		playIn:   playIn,
		rt:       openairt.NewClientWithConfig(rtConf),
	}
}

// Connect dials the server, waits for the session to be created, then sends the
// session configuration (instructions, audio formats, VAD, tools).
func (c *Client) Connect(ctx context.Context) error {
	opts := []openairt.ConnectOption{}
	if c.cfg.Model != "" {
		opts = append(opts, openairt.WithModel(c.cfg.Model))
	}
	conn, err := c.rt.Connect(ctx, opts...)
	if err != nil {
		return fmt.Errorf("realtime connect: %w", err)
	}
	c.conn = conn

	if err := c.waitForSession(ctx); err != nil {
		return fmt.Errorf("wait for session: %w", err)
	}
	if err := c.updateSession(ctx); err != nil {
		return fmt.Errorf("update session: %w", err)
	}
	return nil
}

func (c *Client) waitForSession(ctx context.Context) error {
	for {
		msg, err := c.conn.ReadMessage(ctx)
		if err != nil {
			var permanent *openairt.PermanentError
			if errors.As(err, &permanent) {
				return err
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			time.Sleep(250 * time.Millisecond)
			continue
		}
		switch msg.ServerEventType() {
		case openairt.ServerEventTypeError:
			log.Println("realtime: server error:", msg.(openairt.ErrorEvent).Error.Message)
		case openairt.ServerEventTypeSessionCreated, openairt.ServerEventTypeSessionUpdated:
			return nil
		}
	}
}

func (c *Client) updateSession(ctx context.Context) error {
	voice := openairt.Voice("")
	if c.cfg.Voice != "" {
		voice = openairt.Voice(c.cfg.Voice)
	}
	return c.conn.SendMessage(ctx, openairt.SessionUpdateEvent{
		EventBase: openairt.EventBase{EventID: "session-init"},
		Session: openairt.SessionUnion{
			Realtime: &openairt.RealtimeSession{
				Instructions: c.cfg.Instructions,
				Audio: &openairt.RealtimeSessionAudio{
					Input: &openairt.SessionAudioInput{
						Format: &openairt.AudioFormatUnion{
							PCM: &openairt.AudioFormatPCM{Rate: c.cfg.SampleRate},
						},
						TurnDetection: &openairt.TurnDetectionUnion{
							ServerVad: &openairt.ServerVad{},
						},
					},
					Output: &openairt.SessionAudioOutput{
						Voice: voice,
						Format: &openairt.AudioFormatUnion{
							PCM: &openairt.AudioFormatPCM{Rate: c.cfg.SampleRate},
						},
					},
				},
				Tools: c.registry.ToolUnions(),
			},
		},
	})
}

// SendAudio appends a PCM16 chunk to the input audio buffer.
func (c *Client) SendAudio(ctx context.Context, pcm []byte) error {
	if len(pcm) == 0 {
		return nil
	}
	return c.conn.SendMessage(ctx, openairt.InputAudioBufferAppendEvent{
		EventBase: openairt.EventBase{EventID: "audio"},
		Audio:     base64.StdEncoding.EncodeToString(pcm),
	})
}

// Run reads server events until the context is cancelled or the connection
// fails permanently.
func (c *Client) Run(ctx context.Context) error {
	for {
		msg, err := c.conn.ReadMessage(ctx)
		if err != nil {
			var permanent *openairt.PermanentError
			if errors.As(err, &permanent) {
				return fmt.Errorf("connection failed: %w", err)
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			log.Println("realtime: read error, retrying:", err)
			continue
		}

		switch msg.ServerEventType() {
		case openairt.ServerEventTypeInputAudioBufferSpeechStarted:
			log.Println("realtime: speech detected")
		case openairt.ServerEventTypeInputAudioBufferSpeechStopped:
			log.Println("realtime: speech stopped")
		case openairt.ServerEventTypeConversationItemInputAudioTranscriptionCompleted:
			ev := msg.(openairt.ConversationItemInputAudioTranscriptionCompletedEvent)
			log.Printf("you said: %s", ev.Transcript)
		case openairt.ServerEventTypeResponseCreated:
			log.Println("realtime: generating response")
		case openairt.ServerEventTypeResponseOutputAudioDelta:
			ev := msg.(openairt.ResponseOutputAudioDeltaEvent)
			pcm, err := base64.StdEncoding.DecodeString(ev.Delta)
			if err != nil {
				log.Println("realtime: decode audio delta:", err)
				continue
			}
			select {
			case c.playIn <- pcm:
			default:
				log.Println("realtime: dropped playback chunk")
			}
		case openairt.ServerEventTypeResponseFunctionCallArgumentsDone:
			c.handleFunctionCall(ctx, msg.(openairt.ResponseFunctionCallArgumentsDoneEvent))
		case openairt.ServerEventTypeError:
			log.Println("realtime: server error:", msg.(openairt.ErrorEvent).Error.Message)
		}
	}
}

func (c *Client) handleFunctionCall(ctx context.Context, ev openairt.ResponseFunctionCallArgumentsDoneEvent) {
	log.Printf("tool call: %s(%s)", ev.Name, ev.Arguments)
	tool, ok := c.registry.Get(ev.Name)
	if !ok {
		log.Printf("realtime: unknown tool %q", ev.Name)
		return
	}
	output, err := tool.Execute(ctx, ev.Arguments)
	if err != nil {
		log.Printf("realtime: tool %s failed: %v", ev.Name, err)
		output = fmt.Sprintf("error: %v", err)
	}
	if err := c.conn.SendMessage(ctx, openairt.ConversationItemCreateEvent{
		Item: openairt.MessageItemUnion{
			FunctionCallOutput: &openairt.MessageItemFunctionCallOutput{
				CallID: ev.CallID,
				Output: output,
			},
		},
	}); err != nil {
		log.Println("realtime: send function output:", err)
		return
	}
	// Ask the model to speak the tool result.
	if err := c.conn.SendMessage(ctx, openairt.ResponseCreateEvent{}); err != nil {
		log.Println("realtime: trigger response:", err)
	}
}
```

- [ ] **Step 2: Verify it builds**

Run: `go build ./realtime/`
Expected: exit 0. If a field/type name mismatches the fork, read the vendored source: `go doc github.com/WqyJh/go-openai-realtime/v2` and the type, e.g. `go doc github.com/WqyJh/go-openai-realtime/v2.RealtimeSession`, then correct the offending line. The verbatim VoxInput usage in the spec is the reference.

- [ ] **Step 3: Commit**

```bash
git add realtime/client.go
git commit -m "feat: realtime client connect, session, and event loop"
```

---

## Task 6: CLI wiring

**Files:**
- Create: `cmd/assistant/main.go`

- [ ] **Step 1: Write the implementation**

`cmd/assistant/main.go`:

```go
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mudler/minimal-realtime-assistant/audio"
	"github.com/mudler/minimal-realtime-assistant/realtime"
	"github.com/mudler/minimal-realtime-assistant/tools"
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	wsURL := flag.String("ws-url", env("OPENAI_WS_BASE_URL", "ws://localhost:8080/v1/realtime"), "LocalAI realtime WebSocket URL")
	apiKey := flag.String("api-key", env("OPENAI_API_KEY", "sk-xxx"), "API key (LocalAI ignores it)")
	model := flag.String("model", env("ASSISTANT_MODEL", "gpt-4o-realtime-preview"), "realtime model name served by LocalAI")
	voice := flag.String("voice", env("ASSISTANT_VOICE", ""), "TTS voice (empty = server default)")
	instructions := flag.String("instructions", env("ASSISTANT_INSTRUCTIONS",
		"You are a helpful voice assistant. Keep replies short and conversational. Use the get_weather tool when the user asks about the weather."),
		"system instructions")
	sampleRate := flag.Int("sample-rate", 24000, "PCM sample rate (Hz)")
	flag.Parse()

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	micOut := make(chan []byte, 64)
	playIn := make(chan []byte, 256)

	// Audio device runs for the lifetime of the session.
	go func() {
		if err := audio.Duplex(ctx, *sampleRate, micOut, playIn); err != nil {
			log.Println("audio:", err)
			cancel()
		}
	}()

	// Tools.
	registry := realtime.NewRegistry()
	weather, err := tools.NewWeather()
	if err != nil {
		log.Fatalln("init weather tool:", err)
	}
	registry.Register(weather)

	// Client.
	client := realtime.NewClient(realtime.Config{
		WSURL:        *wsURL,
		APIKey:       *apiKey,
		Model:        *model,
		Voice:        *voice,
		Instructions: *instructions,
		SampleRate:   *sampleRate,
		Timeout:      30 * time.Second,
	}, registry, playIn)

	if err := client.Connect(ctx); err != nil {
		log.Fatalln("connect:", err)
	}
	log.Printf("connected to %s — start talking (Ctrl-C to quit)", *wsURL)

	// Forward captured mic audio to the server.
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case chunk := <-micOut:
				if err := client.SendAudio(ctx, chunk); err != nil {
					log.Println("send audio:", err)
				}
			}
		}
	}()

	if err := client.Run(ctx); err != nil && ctx.Err() == nil {
		log.Println("run:", err)
	}
}
```

- [ ] **Step 2: Verify the whole module builds and vets**

Run: `go build ./... && go vet ./...`
Expected: exit 0, no output.

- [ ] **Step 3: Run the full test suite**

Run: `go test ./...`
Expected: PASS for `realtime` and `tools`; `audio` and `cmd/assistant` report `[no test files]`.

- [ ] **Step 4: Commit**

```bash
git add cmd/assistant/main.go
git commit -m "feat: assistant CLI wiring"
```

---

## Task 7: README + manual end-to-end verification

**Files:**
- Create: `README.md`

- [ ] **Step 1: Write `README.md`**

````markdown
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
- A running LocalAI instance serving a realtime-capable model.

## Build

```bash
CGO_ENABLED=1 go build -o assistant ./cmd/assistant
```

## Run

```bash
./assistant \
  -ws-url ws://localhost:8080/v1/realtime \
  -model gpt-4o-realtime-preview
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
````

- [ ] **Step 2: Commit**

```bash
git add README.md
git commit -m "docs: README with build and run instructions"
```

- [ ] **Step 3: Manual end-to-end verification (requires LocalAI)**

Start LocalAI with a realtime model, then:

```bash
CGO_ENABLED=1 go build -o assistant ./cmd/assistant
./assistant -ws-url ws://localhost:8080/v1/realtime -model <your-realtime-model>
```

Verify, watching the logs:
1. `connected to ws://... — start talking` appears.
2. Speak a sentence → `speech detected` then `speech stopped` then `you said: ...`.
3. The assistant's reply plays back through the speaker (audio deltas).
4. Ask about the weather → a `tool call: get_weather(...)` log line, then the
   model speaks the weather result.

If any step fails, use superpowers:systematic-debugging. Common issues: wrong
model name (server error event), no audio device / ALSA permissions (audio init
error), wrong sample rate (garbled/fast/slow playback — must match the model's
output rate).

---

## Self-review notes

- **Spec coverage:** audio duplex (Task 4), realtime client + session + event loop + tool routing (Task 5), tool interface/registry (Task 2), get_weather (Task 3), config flags/env (Task 6), README + manual e2e (Task 7), `replace` fork pin (Task 1). All spec sections mapped.
- **Type consistency:** `Tool` (Name/Description/Parameters/Execute) is defined in Task 2 and implemented by `Weather` in Task 3; `Registry.Get`/`ToolUnions` used in Task 5; `realtime.Config` fields match `NewClient` usage in Task 6; channels are `chan []byte` throughout.
- **No live-server unit tests** for `audio`/`realtime`/`cmd` by design — gated on `go build`/`go vet` and the Task 7 manual checklist.
