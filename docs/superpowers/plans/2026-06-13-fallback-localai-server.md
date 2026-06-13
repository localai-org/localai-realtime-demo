# Fallback LocalAI Server Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add automatic failover from a primary (typically remote) LocalAI realtime endpoint to a local fallback, with an audible tone on each switch and capped-backoff retry forever.

**Architecture:** A new `realtime.Supervisor` owns endpoint selection in a forever-loop: it tries each `Endpoint` in order (primary → fallback), runs the connected session until it ends, and restarts the list from the top on any failure — which also yields automatic fail-back to the primary. A `Dialer` seam (dial + `Connect`) and an injectable `sleep` make the loop unit-testable with a fake dialer/clock. `cmd/assistant` builds the endpoint list from flags, wires mic routing across reconnects via an `OnConnect` callback, and plays a synthesized sine-sweep tone via `OnSwitch`.

**Tech Stack:** Go 1.24, standard library only for new code (`math`, `time`, `context`, `sync`). Existing deps: `github.com/WqyJh/go-openai-realtime/v2`, `malgo` (audio). Tests use the standard `testing` package (no testify), matching the existing style.

---

## File Structure

- `audio/tone.go` — new. `ToneSweep` pure function: generates a PCM16 sine sweep. One responsibility: produce an audible cue as raw samples.
- `audio/tone_test.go` — new. Tests for `ToneSweep`.
- `realtime/supervisor.go` — new. `Endpoint`, `Session`, `Dialer`, `BackoffPolicy`, `Supervisor` + the failover loop. One responsibility: orchestrate connect/reconnect/failover.
- `realtime/supervisor_test.go` — new. Supervisor loop tests with a scripted fake dialer and fake clock.
- `cmd/assistant/main.go` — modify. Add fallback flags, build the `[]Endpoint`, construct the production `Dialer`, wire `OnConnect` (mic routing) and `OnSwitch` (tones), run the Supervisor instead of the one-shot connect/run.
- `README.md` — modify. Document the fallback flags and behavior.

The existing `realtime.Client` already satisfies the new `Session` interface (`Run(ctx) error` and `SendAudio(ctx, pcm) error` both exist), so no change to `client.go` is needed.

---

## Task 1: Tone generator

**Files:**
- Create: `audio/tone.go`
- Test: `audio/tone_test.go`

- [ ] **Step 1: Write the failing tests**

Create `audio/tone_test.go`:

```go
package audio

import (
	"testing"
	"time"
)

func TestToneSweepLength(t *testing.T) {
	b := ToneSweep(24000, 440, 660, 200*time.Millisecond)
	// 24000 Hz * 0.2 s = 4800 samples, 2 bytes/sample (PCM16 mono).
	want := 4800 * 2
	if len(b) != want {
		t.Fatalf("len = %d, want %d", len(b), want)
	}
}

func TestToneSweepNonSilent(t *testing.T) {
	b := ToneSweep(24000, 440, 660, 200*time.Millisecond)
	nonzero := false
	for _, by := range b {
		if by != 0 {
			nonzero = true
			break
		}
	}
	if !nonzero {
		t.Fatal("tone is entirely silent")
	}
}

func TestToneSweepZeroDurationIsNil(t *testing.T) {
	if b := ToneSweep(24000, 440, 660, 0); b != nil {
		t.Fatalf("zero duration: got %d bytes, want nil", len(b))
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./audio/ -run TestToneSweep -v`
Expected: FAIL — `undefined: ToneSweep`.

- [ ] **Step 3: Implement `ToneSweep`**

Create `audio/tone.go`:

```go
package audio

import (
	"math"
	"time"
)

// ToneSweep generates a linear-frequency sine sweep from startHz to endHz over
// dur, returned as little-endian PCM16 mono at sampleRate. It is used as an
// audible cue when the assistant switches between realtime endpoints: an
// ascending sweep for "recovered toward primary", a descending one for "dropped
// to fallback". A short fade in/out avoids clicks. Returns nil for non-positive
// durations.
func ToneSweep(sampleRate int, startHz, endHz float64, dur time.Duration) []byte {
	n := int(float64(sampleRate) * dur.Seconds())
	if n <= 0 {
		return nil
	}
	const amp = 0.3 // keep the cue well below full scale
	out := make([]byte, n*2)
	phase := 0.0
	fade := n / 10
	for i := 0; i < n; i++ {
		frac := float64(i) / float64(n)
		freq := startHz + (endHz-startHz)*frac
		phase += 2 * math.Pi * freq / float64(sampleRate)

		env := 1.0
		if fade > 0 {
			if i < fade {
				env = float64(i) / float64(fade)
			} else if i >= n-fade {
				env = float64(n-1-i) / float64(fade)
			}
		}

		s := int16(amp * env * math.Sin(phase) * math.MaxInt16)
		out[2*i] = byte(s)
		out[2*i+1] = byte(s >> 8)
	}
	return out
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./audio/ -run TestToneSweep -v`
Expected: PASS (all three).

- [ ] **Step 5: Commit**

```bash
git add audio/tone.go audio/tone_test.go
git commit -m "feat: ToneSweep audible cue generator for endpoint switches"
```

---

## Task 2: Supervisor failover loop

**Files:**
- Create: `realtime/supervisor.go`
- Test: `realtime/supervisor_test.go`

- [ ] **Step 1: Write the failing tests**

Create `realtime/supervisor_test.go`:

```go
package realtime

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// blockingSession.Run blocks until ctx is cancelled, then returns ctx.Err().
type blockingSession struct{}

func (blockingSession) Run(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}
func (blockingSession) SendAudio(context.Context, []byte) error { return nil }

// endNowSession.Run returns immediately, simulating a dropped connection that
// forces the supervisor to reconnect.
type endNowSession struct{}

func (endNowSession) Run(context.Context) error              { return nil }
func (endNowSession) SendAudio(context.Context, []byte) error { return nil }

func twoEndpoints() []Endpoint {
	return []Endpoint{
		{Name: "primary", WSURL: "ws://primary"},
		{Name: "fallback", WSURL: "ws://fallback"},
	}
}

func TestSupervisorFailsOverToFallback(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var mu sync.Mutex
	var calls []string
	connected := make(chan Session, 1)

	sup := &Supervisor{
		Endpoints: twoEndpoints(),
		Dial: func(_ context.Context, ep Endpoint) (Session, error) {
			mu.Lock()
			calls = append(calls, ep.Name)
			mu.Unlock()
			if ep.Name == "primary" {
				return nil, errors.New("primary down")
			}
			return blockingSession{}, nil
		},
		OnConnect: func(s Session) { connected <- s },
		Backoff:   BackoffPolicy{Min: time.Second, Max: 30 * time.Second},
	}

	done := make(chan error, 1)
	go func() { done <- sup.Run(ctx) }()

	select {
	case <-connected:
	case <-time.After(time.Second):
		t.Fatal("never connected to fallback")
	}

	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run returned %v, want context.Canceled", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 2 || calls[0] != "primary" || calls[1] != "fallback" {
		t.Fatalf("dial order = %v, want [primary fallback]", calls)
	}
}

func TestSupervisorFirstConnectPlaysNoTone(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	switched := make(chan [2]string, 4)
	connected := make(chan Session, 1)

	sup := &Supervisor{
		Endpoints: twoEndpoints(),
		Dial: func(_ context.Context, ep Endpoint) (Session, error) {
			if ep.Name == "primary" {
				return blockingSession{}, nil
			}
			return nil, errors.New("unused")
		},
		OnConnect: func(s Session) { connected <- s },
		OnSwitch: func(from, to *Endpoint) {
			f := ""
			if from != nil {
				f = from.Name
			}
			switched <- [2]string{f, to.Name}
		},
		Backoff: BackoffPolicy{Min: time.Second, Max: 30 * time.Second},
	}

	done := make(chan error, 1)
	go func() { done <- sup.Run(ctx) }()

	<-connected
	cancel()
	<-done

	select {
	case s := <-switched:
		t.Fatalf("OnSwitch fired on first connect: %v", s)
	default:
	}
}

func TestSupervisorRecoversToPrimaryFiresSwitch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var mu sync.Mutex
	primaryAttempts := 0
	switched := make(chan [2]string, 4)

	sup := &Supervisor{
		Endpoints: twoEndpoints(),
		Dial: func(_ context.Context, ep Endpoint) (Session, error) {
			mu.Lock()
			defer mu.Unlock()
			if ep.Name == "primary" {
				primaryAttempts++
				if primaryAttempts == 1 {
					return nil, errors.New("primary down")
				}
				return blockingSession{}, nil // recovered
			}
			return endNowSession{}, nil // fallback connects, then drops
		},
		OnSwitch: func(from, to *Endpoint) {
			f := ""
			if from != nil {
				f = from.Name
			}
			switched <- [2]string{f, to.Name}
		},
		Backoff: BackoffPolicy{Min: time.Second, Max: 30 * time.Second},
	}

	done := make(chan error, 1)
	go func() { done <- sup.Run(ctx) }()

	select {
	case s := <-switched:
		if s != [2]string{"fallback", "primary"} {
			t.Fatalf("first switch = %v, want [fallback primary]", s)
		}
	case <-time.After(time.Second):
		t.Fatal("never switched back to primary")
	}

	cancel()
	<-done
}

func TestSupervisorBacksOffWhenAllFail(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var mu sync.Mutex
	var delays []time.Duration

	sup := &Supervisor{
		Endpoints: twoEndpoints(),
		Dial: func(_ context.Context, _ Endpoint) (Session, error) {
			return nil, errors.New("all down")
		},
		Backoff: BackoffPolicy{Min: time.Second, Max: 30 * time.Second},
		Sleep: func(_ context.Context, d time.Duration) {
			mu.Lock()
			delays = append(delays, d)
			n := len(delays)
			mu.Unlock()
			if n >= 3 {
				cancel()
			}
		},
	}

	if err := sup.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run returned %v, want context.Canceled", err)
	}

	mu.Lock()
	defer mu.Unlock()
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}
	if len(delays) != 3 {
		t.Fatalf("delays = %v, want 3 entries", delays)
	}
	for i := range want {
		if delays[i] != want[i] {
			t.Fatalf("delays = %v, want %v (doubling backoff)", delays, want)
		}
	}
}

func TestSupervisorResetsBackoffAfterSuccess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var mu sync.Mutex
	var delays []time.Duration
	primaryUp := false

	sup := &Supervisor{
		Endpoints: twoEndpoints(),
		Dial: func(_ context.Context, ep Endpoint) (Session, error) {
			mu.Lock()
			defer mu.Unlock()
			if ep.Name == "primary" && primaryUp {
				primaryUp = false // one-shot success, then drops
				return endNowSession{}, nil
			}
			return nil, errors.New("down")
		},
		Backoff: BackoffPolicy{Min: time.Second, Max: 30 * time.Second},
		Sleep: func(_ context.Context, d time.Duration) {
			mu.Lock()
			delays = append(delays, d)
			n := len(delays)
			if n == 1 {
				primaryUp = true // next pass: primary connects, resetting backoff
			}
			mu.Unlock()
			if n == 2 {
				cancel()
			}
		},
	}

	if err := sup.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run returned %v, want context.Canceled", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(delays) != 2 {
		t.Fatalf("delays = %v, want 2 entries", delays)
	}
	// Second backoff must be Min again (reset by the successful connect),
	// not 2*Min as it would be without a reset.
	if delays[0] != time.Second || delays[1] != time.Second {
		t.Fatalf("delays = %v, want [1s 1s] (reset after success)", delays)
	}
}

func TestSupervisorReturnsWhenContextAlreadyCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	dialed := false
	sup := &Supervisor{
		Endpoints: twoEndpoints(),
		Dial: func(_ context.Context, _ Endpoint) (Session, error) {
			dialed = true
			return blockingSession{}, nil
		},
	}

	if err := sup.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run returned %v, want context.Canceled", err)
	}
	if dialed {
		t.Fatal("dialed despite cancelled context")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./realtime/ -run TestSupervisor -v`
Expected: FAIL — `undefined: Supervisor`, `undefined: Endpoint`, etc.

- [ ] **Step 3: Implement the Supervisor**

Create `realtime/supervisor.go`:

```go
package realtime

import (
	"context"
	"log"
	"time"
)

// Compile-time guard: the realtime client must satisfy Session.
var _ Session = (*Client)(nil)

// Endpoint describes one LocalAI realtime server. Name is used in logs and to
// decide tone direction; WSURL/Model/APIKey may differ per endpoint.
type Endpoint struct {
	Name   string
	WSURL  string
	Model  string
	APIKey string
}

// Session is a connected, ready realtime session. *Client satisfies it.
type Session interface {
	Run(ctx context.Context) error // blocks until the connection ends
	SendAudio(ctx context.Context, pcm []byte) error
}

// Dialer establishes a ready session for an endpoint (dial + Connect + session
// setup). It returns an error if the endpoint is unreachable or setup fails.
type Dialer func(ctx context.Context, ep Endpoint) (Session, error)

// BackoffPolicy bounds the wait between fully-failed passes over the endpoint
// list. The delay starts at Min, doubles each failed pass, and is capped at Max.
type BackoffPolicy struct {
	Min time.Duration
	Max time.Duration
}

// Supervisor connects to endpoints in order, runs the live session until it
// ends, and reconnects from the top of the list on any failure — giving fast
// failover, automatic fail-back to the primary, and capped-backoff retry when
// the whole list is unreachable. It runs until ctx is cancelled.
type Supervisor struct {
	Endpoints []Endpoint
	Dial      Dialer

	// OnConnect is called with the live session on every successful connection
	// so the caller can route mic audio to it. Optional.
	OnConnect func(Session)
	// OnSwitch is called only when the connected endpoint changes (never on the
	// first connection, where there is nothing to switch from). from is non-nil.
	// Optional.
	OnSwitch func(from, to *Endpoint)

	Backoff BackoffPolicy
	// Sleep waits for d or until ctx is cancelled. Defaults to a ctx-aware sleep;
	// overridden in tests.
	Sleep func(ctx context.Context, d time.Duration)
}

// Run drives the failover loop until ctx is cancelled, then returns ctx.Err().
func (s *Supervisor) Run(ctx context.Context) error {
	if s.Sleep == nil {
		s.Sleep = ctxSleep
	}
	if s.Backoff.Min <= 0 {
		s.Backoff.Min = time.Second
	}
	if s.Backoff.Max <= 0 {
		s.Backoff.Max = 30 * time.Second
	}

	delay := s.Backoff.Min
	var last *Endpoint

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		connected := false
		for i := range s.Endpoints {
			ep := s.Endpoints[i]
			sess, err := s.Dial(ctx, ep)
			if err != nil {
				if cerr := ctx.Err(); cerr != nil {
					return cerr
				}
				log.Printf("supervisor: endpoint %q connect failed: %v", ep.Name, err)
				continue
			}

			connected = true
			delay = s.Backoff.Min

			if last != nil && last.WSURL != ep.WSURL && s.OnSwitch != nil {
				s.OnSwitch(last, &s.Endpoints[i])
			}
			last = &s.Endpoints[i]
			if s.OnConnect != nil {
				s.OnConnect(sess)
			}

			rerr := sess.Run(ctx)
			if cerr := ctx.Err(); cerr != nil {
				return cerr
			}
			log.Printf("supervisor: endpoint %q session ended: %v; reconnecting", ep.Name, rerr)
			break // restart the endpoint list from the top
		}

		if !connected {
			s.Sleep(ctx, delay)
			if err := ctx.Err(); err != nil {
				return err
			}
			delay *= 2
			if delay > s.Backoff.Max {
				delay = s.Backoff.Max
			}
		}
	}
}

func ctxSleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./realtime/ -run TestSupervisor -v`
Expected: PASS (all six tests).

- [ ] **Step 5: Run the full realtime package tests and vet**

Run: `go test ./realtime/ && go vet ./realtime/`
Expected: PASS, no vet warnings. (Confirms `*Client` still satisfies `Session` and nothing else broke.)

- [ ] **Step 6: Commit**

```bash
git add realtime/supervisor.go realtime/supervisor_test.go
git commit -m "feat: Supervisor with primary/fallback endpoint failover"
```

---

## Task 3: Wire the Supervisor into the assistant

**Files:**
- Modify: `cmd/assistant/main.go`

- [ ] **Step 1: Add the fallback flags**

In `cmd/assistant/main.go`, inside `main()`, immediately after the existing `aecDelayMs` flag (before `flag.Parse()`), add:

```go
	fallbackWSURL := flag.String("fallback-ws-url", env("FALLBACK_WS_BASE_URL", "ws://localhost:8080/v1/realtime"), "fallback LocalAI realtime WebSocket URL")
	fallbackModel := flag.String("fallback-model", env("FALLBACK_MODEL", ""), "fallback realtime model (empty = same as -model)")
	fallbackAPIKey := flag.String("fallback-api-key", env("FALLBACK_API_KEY", ""), "fallback API key (empty = same as -api-key)")
```

- [ ] **Step 2: Add the `sync` import**

In the import block of `cmd/assistant/main.go`, add `"sync"` (keep imports ordered: it goes after `"strconv"` and before `"syscall"`).

- [ ] **Step 3: Replace the client/connect/run orchestration with the Supervisor**

In `cmd/assistant/main.go`, replace everything from the `// Client.` comment through the end of `main()` (the `realtime.NewClient(...)` construction, the `client.Connect` call and its log line, the mic-forwarding goroutine, and the final `client.Run` block — original lines 122-156) with:

```go
	// Endpoints: primary first, then fallback. Failover walks this list from the
	// top on every (re)connect, which also gives automatic fail-back to primary.
	endpoints := []realtime.Endpoint{
		{Name: "primary", WSURL: *wsURL, Model: *model, APIKey: *apiKey},
		{Name: "fallback", WSURL: *fallbackWSURL, Model: orDefault(*fallbackModel, *model), APIKey: orDefault(*fallbackAPIKey, *apiKey)},
	}
	if endpoints[0].WSURL == endpoints[1].WSURL &&
		endpoints[0].Model == endpoints[1].Model &&
		endpoints[0].APIKey == endpoints[1].APIKey {
		log.Println("fallback endpoint is identical to primary; failover is a no-op")
	}

	// Current connected session, swapped on each (re)connect. The mic-forwarding
	// goroutine routes captured audio to whichever session is live; chunks
	// captured while reconnecting are dropped (context is lost on switch).
	var (
		curMu sync.Mutex
		cur   realtime.Session
	)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case chunk := <-micOut:
				curMu.Lock()
				s := cur
				curMu.Unlock()
				if s == nil {
					continue
				}
				if err := s.SendAudio(ctx, chunk); err != nil {
					log.Println("send audio:", err)
				}
			}
		}
	}()

	sup := &realtime.Supervisor{
		Endpoints: endpoints,
		Dial: func(ctx context.Context, ep realtime.Endpoint) (realtime.Session, error) {
			client := realtime.NewClient(realtime.Config{
				WSURL:        ep.WSURL,
				APIKey:       ep.APIKey,
				Model:        ep.Model,
				Voice:        *voice,
				Instructions: *instructions,
				Language:     *language,
				SampleRate:   *sampleRate,
				Timeout:      30 * time.Second,
			}, registry, player)
			if err := client.Connect(ctx); err != nil {
				return nil, err
			}
			log.Printf("connected to %s (%s)", ep.WSURL, ep.Name)
			return client, nil
		},
		OnConnect: func(s realtime.Session) {
			curMu.Lock()
			cur = s
			curMu.Unlock()
		},
		OnSwitch: func(from, to *realtime.Endpoint) {
			// Ascending sweep when moving toward the primary (lower index),
			// descending toward the fallback. from is non-nil here.
			if endpointIndex(endpoints, to) < endpointIndex(endpoints, from) {
				player.Write(audio.ToneSweep(*sampleRate, 440, 660, 200*time.Millisecond))
			} else {
				player.Write(audio.ToneSweep(*sampleRate, 660, 440, 200*time.Millisecond))
			}
		},
		Backoff: realtime.BackoffPolicy{Min: time.Second, Max: 30 * time.Second},
	}

	log.Printf("starting (primary=%s fallback=%s) — start talking (Ctrl-C to quit)", *wsURL, *fallbackWSURL)
	if err := sup.Run(ctx); err != nil && ctx.Err() == nil {
		log.Println("supervisor:", err)
	}
}

func orDefault(v, def string) string {
	if v != "" {
		return v
	}
	return def
}

// endpointIndex returns the position of ep within eps by pointer identity. The
// Supervisor passes pointers into this same slice, so identity is exact.
func endpointIndex(eps []realtime.Endpoint, ep *realtime.Endpoint) int {
	for i := range eps {
		if &eps[i] == ep {
			return i
		}
	}
	return -1
}
```

Note: `audio` and `context` are already imported in `main.go`; no new import beyond `sync` (Step 2) is required.

- [ ] **Step 4: Verify the build**

Run: `CGO_ENABLED=1 go build -o /tmp/assistant ./cmd/assistant`
Expected: builds with no errors.

- [ ] **Step 5: Verify vet and the full test suite**

Run: `go vet ./... && go test ./...`
Expected: no vet warnings; all tests PASS.

- [ ] **Step 6: Smoke-check the new flags appear**

Run: `/tmp/assistant -h 2>&1 | grep -E 'fallback-(ws-url|model|api-key)'`
Expected: the three `-fallback-*` flags are listed.

- [ ] **Step 7: Commit**

```bash
git add cmd/assistant/main.go
git commit -m "feat: run assistant through endpoint-failover Supervisor"
```

---

## Task 4: Document the fallback behavior

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Read the current Configuration section**

Run: `sed -n '/## Configuration/,/^## /p' README.md`
Expected: shows the existing flag table and any following prose, so the new rows match its exact column format.

- [ ] **Step 2: Add the fallback flags to the configuration table**

In `README.md`, add these rows to the existing Configuration flag table (match the table's exact column layout — the table header is `| Flag | Env | Default |`):

```markdown
| `-fallback-ws-url`  | `FALLBACK_WS_BASE_URL`   | `ws://localhost:8080/v1/realtime`  |
| `-fallback-model`   | `FALLBACK_MODEL`         | (same as `-model`)                 |
| `-fallback-api-key` | `FALLBACK_API_KEY`       | (same as `-api-key`)               |
```

- [ ] **Step 3: Add a "Fallback / failover" subsection**

In `README.md`, immediately after the Configuration table, add:

```markdown
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
```

- [ ] **Step 4: Commit**

```bash
git add README.md
git commit -m "docs: document primary/fallback failover"
```

---

## Self-Review Notes

- **Spec coverage:** Endpoint model + per-endpoint model/key → Task 3 Step 1/3. Failover loop, fast-failover-then-backoff, fail-back, ctx-cancel exit → Task 2. Tones (two directions, synthesized, no assets) → Task 1 + Task 3 OnSwitch. Mic/player reuse across reconnects via OnConnect → Task 3. Config flags + no-op detection → Task 3. README → Task 4. All spec sections map to a task.
- **Type consistency:** `Session{Run(ctx)error, SendAudio(ctx,[]byte)error}`, `Dialer func(ctx,Endpoint)(Session,error)`, `Supervisor{Endpoints,Dial,OnConnect,OnSwitch,Backoff,Sleep}`, `BackoffPolicy{Min,Max}`, `ToneSweep(int,float64,float64,time.Duration)[]byte` are used identically across the supervisor, its tests, and `main.go`. `*Client` satisfies `Session` (guard in `supervisor.go`).
- **OnSwitch contract:** never called on first connect (guarded by `last != nil`), so the `from` pointer is always non-nil when the tone logic in `main.go` dereferences it via `endpointIndex`.
