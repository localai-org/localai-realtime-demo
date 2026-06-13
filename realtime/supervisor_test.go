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
func (blockingSession) Close() error                            { return nil }

// endNowSession.Run returns immediately, simulating a dropped connection that
// forces the supervisor to reconnect.
type endNowSession struct{}

func (endNowSession) Run(context.Context) error              { return nil }
func (endNowSession) SendAudio(context.Context, []byte) error { return nil }
func (endNowSession) Close() error                            { return nil }

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
		Sleep:   func(context.Context, time.Duration) {},
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

func TestSupervisorEmptyEndpointsReturnsError(t *testing.T) {
	dialed := false
	sup := &Supervisor{
		Endpoints: nil,
		Dial: func(context.Context, Endpoint) (Session, error) {
			dialed = true
			return blockingSession{}, nil
		},
	}
	if err := sup.Run(context.Background()); err == nil {
		t.Fatal("Run returned nil, want an error for empty endpoints")
	}
	if dialed {
		t.Fatal("dialed despite empty endpoint list")
	}
}

// closeRecorderSession ends immediately and records that Close was called.
type closeRecorderSession struct{ closed *int }

func (closeRecorderSession) Run(context.Context) error               { return nil }
func (closeRecorderSession) SendAudio(context.Context, []byte) error { return nil }
func (s closeRecorderSession) Close() error                          { *s.closed++; return nil }

func TestSupervisorClosesSessionAndFiresOnDisconnect(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	closed := 0
	disconnects := 0

	sup := &Supervisor{
		Endpoints: twoEndpoints(),
		Dial: func(context.Context, Endpoint) (Session, error) {
			return closeRecorderSession{closed: &closed}, nil
		},
		OnDisconnect: func() { disconnects++ },
		Backoff:      BackoffPolicy{Min: time.Second, Max: 30 * time.Second},
		Sleep: func(context.Context, time.Duration) {
			cancel() // stop after the first connect+disconnect cycle
		},
	}

	if err := sup.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run returned %v, want context.Canceled", err)
	}
	if closed < 1 {
		t.Fatalf("session Close called %d times, want >= 1", closed)
	}
	if disconnects < 1 {
		t.Fatalf("OnDisconnect called %d times, want >= 1", disconnects)
	}
}
