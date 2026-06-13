# Fallback LocalAI Server — Design

**Date:** 2026-06-13
**Status:** Approved (pending spec review)

## Problem

The assistant connects to a single LocalAI realtime endpoint (`-ws-url`). When
that endpoint is unreachable — e.g. the primary is a remote server and the
internet drops — the session dies and the app exits. We want a primary endpoint
(typically remote) with an automatic fallback to a local docker-compose
instance, so the assistant keeps working when connectivity is lost.

## Goals

- Try a **primary** endpoint first; fall back to a **local** endpoint when the
  primary can't be reached.
- Failover happens at **(re)connect time**. A live session is stateful and
  conversation context is **lost on switch** — this is acceptable.
- A mid-conversation connection drop (e.g. internet goes down while speaking) is
  treated like any other connection failure: it triggers a reconnect, and the
  reconnect walks the endpoint list from the top.
- Restarting the endpoint list from the top on every reconnect gives automatic
  **fail-back** to the primary once it is healthy again.
- Give the user **audible feedback** (a distinct tone per direction) whenever the
  connected endpoint changes.
- Never exit on a transient outage: retry forever with capped backoff until a
  connection succeeds or the user quits (Ctrl-C / SIGTERM).

## Non-Goals

- No seamless mid-call handoff / conversation-state preservation across switches.
- No support for more than the two endpoints (primary + fallback) in this
  iteration. An ordered list of N endpoints is a possible future extension; the
  Supervisor is built around a slice so this is not precluded, but only two are
  wired up.
- No spoken/TTS switch announcement — tones only.

## Endpoint Model & Configuration

Each server is described by an `Endpoint`:

```go
type Endpoint struct {
    Name   string // "primary" / "fallback" — used in logs & tone direction
    WSURL  string
    Model  string
    APIKey string
}
```

`cmd/assistant/main.go` builds an ordered `[]Endpoint{primary, fallback}` from
flags/env. Existing primary flags are unchanged; new fallback flags are added.

| Flag | Env | Default |
|---|---|---|
| `-ws-url` | `OPENAI_WS_BASE_URL` | `ws://localhost:8080/v1/realtime` (unchanged) |
| `-model` | `ASSISTANT_MODEL` | `gpt-4o-realtime-preview` (unchanged) |
| `-api-key` | `OPENAI_API_KEY` | `sk-xxx` (unchanged) |
| `-fallback-ws-url` | `FALLBACK_WS_BASE_URL` | `ws://localhost:8080/v1/realtime` |
| `-fallback-model` | `FALLBACK_MODEL` | `""` → reuses `-model` |
| `-fallback-api-key` | `FALLBACK_API_KEY` | `""` → reuses `-api-key` |

Shared per-session settings (voice, instructions, language, sample rate, timeout)
stay global and apply to whichever endpoint is connected.

If the resolved fallback endpoint is identical to the primary (same URL, model,
and key), `main.go` logs that failover is effectively a no-op but still runs
normally.

## Failover / Reconnect Loop

The orchestration moves out of `main.go`'s straight-line connect-then-run into a
supervising loop that owns endpoint selection and lives for the whole process.

```
loop forever (until ctx cancelled):
    connectedThisPass = false
    for each endpoint in [primary, fallback]:
        session, err = dial(ctx, endpoint)   // dial + Connect + session setup
        if err != nil:
            log "endpoint <name> connect failed: <err>"
            continue to next endpoint
        connectedThisPass = true
        if endpoint != lastConnected:
            onSwitch(lastConnected, endpoint)   // play direction tone
        lastConnected = endpoint
        reset backoff to min
        err = session.Run(ctx)                  // blocks until connection ends
        if ctx cancelled: return
        log "endpoint <name> session ended: <err>; reconnecting"
        break   // restart endpoint list from the top
    if not connectedThisPass:
        sleep backoff (interruptible by ctx); backoff = min(backoff*2, max)
```

Behavior:

- **Fast failover within a pass:** each endpoint is tried once; we only back off
  when the *entire* list is unreachable.
- **Fail-back:** because every pass restarts from the top, a recovered primary is
  picked up on the next reconnect.
- **Backoff:** starts at 1s, doubles each fully-failed pass, capped at 30s; reset
  to 1s on any successful connection.
- **Clean shutdown:** `ctx` cancellation (Ctrl-C / SIGTERM) breaks the loop
  promptly at any wait point (dial, Run, or backoff sleep).

The mic-capture channel and the audio `Player` are created **once** in `main.go`
and reused across reconnects; only the `realtime.Client`/WebSocket is rebuilt per
connection. The mic-forwarding goroutine reads from the persistent channel and
writes to whichever client is current.

### Carrying mic + player across reconnects

`main.go` keeps a reference to the "current" client that the mic-forwarding
goroutine sends to. On each new connection the Supervisor must expose the live
client/session so `main.go` can route mic audio to it. Concretely, the `Session`
interface returned by the dialer also provides the audio sink, and the
Supervisor invokes a `onConnect(session)` callback (alongside `onSwitch`) so
`main.go` can atomically swap the current send target. Mic chunks captured while
no connection is active are dropped (best-effort), matching the
context-loss-on-switch decision.

## The Supervisor

New file `realtime/supervisor.go`:

```go
// Session is a connected, ready realtime session.
type Session interface {
    Run(ctx context.Context) error            // blocks until the connection ends
    SendAudio(ctx context.Context, pcm []byte) error
}

// Dialer establishes a ready session for an endpoint (dial + Connect + session
// setup). Returns an error if the endpoint is unreachable or setup fails.
type Dialer func(ctx context.Context, ep Endpoint) (Session, error)

type BackoffPolicy struct {
    Min time.Duration // 1s
    Max time.Duration // 30s
}

type Supervisor struct {
    endpoints []Endpoint
    dial      Dialer
    onConnect func(Session)               // route mic audio to this session
    onSwitch  func(from, to *Endpoint)    // play direction tone; from==nil on first connect (no tone)
    backoff   BackoffPolicy
    sleep     func(ctx context.Context, d time.Duration) // injectable; default ctx-aware sleep
}

func (s *Supervisor) Run(ctx context.Context) error
```

- `*realtime.Client` already satisfies `Session` (`Run`, `SendAudio` exist). The
  production `Dialer` wraps `NewClient(...)` + `Connect(...)` and returns the
  client.
- `onSwitch(from, to)` is called only when the connected endpoint **changes**.
  On the very first successful connection `from` is `nil` and **no tone plays**
  (nothing to switch from). Direction is decided by the endpoints' index in the
  slice (see Tones).
- The `dial` and `sleep` seams make the loop fully unit-testable with a scripted
  fake dialer and a fake clock — no real sockets, no real sleeping.

## Tones

A small helper in the `audio` package synthesizes two short PCM16 sine cues at
the session sample rate and writes them through the existing `Player`:

- **Down-sweep** (~660→440 Hz, ~200 ms): moved *toward* fallback (higher index in
  the endpoint list).
- **Up-sweep** (~440→660 Hz, ~200 ms): moved *toward* primary / recovered (lower
  index).

Direction is derived from the endpoints' positions in the slice: a switch to a
higher index plays the down tone; to a lower index, the up tone. No bundled
assets and no new dependencies — the cue is generated as raw samples.

```go
// ToneSweep returns startHz→endHz over dur as PCM16 mono at sampleRate.
func ToneSweep(sampleRate int, startHz, endHz float64, dur time.Duration) []byte
```

`main.go` wires `onSwitch` to compute the direction from the two endpoints'
indices and write the appropriate sweep to the `Player`.

## Error Handling

- Per-endpoint `dial`/`Connect` failures are logged with the endpoint name and
  are never fatal — the loop moves to the next endpoint or backs off.
- A `Session.Run` returning (connection dropped, permanent error) is logged and
  triggers a reconnect from the top of the list.
- The only clean exit path is `ctx` cancellation, which `Supervisor.Run` returns
  (`ctx.Err()` / `nil`) so `main.go` exits 0 on Ctrl-C.

## Testing

**Supervisor unit tests** (fake `Dialer` + fake `sleep`):

- primary fails → fallback connects.
- both fail → backoff invoked → retry pass → eventually connects.
- backoff doubles on consecutive failed passes and is capped at `Max`.
- backoff resets to `Min` after a successful connection.
- first connection fires `onConnect` but **not** `onSwitch` (from==nil).
- switch to fallback then recovery to primary fires `onSwitch` with the correct
  from/to (so the tone direction can be asserted).
- session drop mid-run → loop restarts the endpoint list from the top.
- ctx cancellation during dial, during Run, and during backoff each return
  promptly.

**Tone generator test** (`audio`):

- output byte length matches `sampleRate * dur` (× 2 bytes/sample, mono).
- output is non-silent (contains non-zero samples).

## Files Touched

- `realtime/supervisor.go` — new: `Endpoint`, `Session`, `Dialer`,
  `BackoffPolicy`, `Supervisor`.
- `realtime/supervisor_test.go` — new: Supervisor unit tests.
- `audio/tone.go` — new: `ToneSweep` helper.
- `audio/tone_test.go` — new: tone generator test.
- `cmd/assistant/main.go` — add fallback flags, build endpoint slice, construct
  the production `Dialer`, wire `onConnect` (mic routing) and `onSwitch` (tones),
  run the Supervisor instead of the one-shot connect/run.
- `README.md` — document the fallback flags and behavior.
