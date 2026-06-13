package realtime

import (
	"context"
	"errors"
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
// Run is single-use: call it once. Endpoints must not be mutated while Run is
// active (OnSwitch receives pointers into the slice).
func (s *Supervisor) Run(ctx context.Context) error {
	if len(s.Endpoints) == 0 {
		return errors.New("supervisor: no endpoints configured")
	}
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
	lastIdx := -1

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
				continue // fast failover: try the next endpoint with no delay
			}

			connected = true
			delay = s.Backoff.Min // a healthy connection resets the backoff

			if lastIdx >= 0 && lastIdx != i && s.OnSwitch != nil {
				s.OnSwitch(&s.Endpoints[lastIdx], &s.Endpoints[i])
			}
			lastIdx = i
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

		// Throttle before the next pass. A short, fixed delay between reconnects
		// (delay == Backoff.Min after a healthy connection) keeps a flapping
		// endpoint — one that accepts then immediately drops — from spinning the
		// loop at 100% CPU. When the whole list is unreachable the delay grows,
		// capped at Backoff.Max.
		s.Sleep(ctx, delay)
		if err := ctx.Err(); err != nil {
			return err
		}
		if !connected {
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
