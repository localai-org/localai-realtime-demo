package realtime

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// fakePinger fails on the failAt-th ping (1-based; 0 = never fails). When block
// is set, each ping waits that long (respecting ctx) before returning, to
// exercise the per-ping timeout.
type fakePinger struct {
	count  atomic.Int32
	failAt int32
	err    error
	block  time.Duration
}

func (f *fakePinger) Ping(ctx context.Context) error {
	c := f.count.Add(1)
	if f.block > 0 {
		select {
		case <-time.After(f.block):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if f.failAt > 0 && c >= f.failAt {
		return f.err
	}
	return nil
}

func TestKeepaliveReturnsErrorWhenPingFails(t *testing.T) {
	p := &fakePinger{failAt: 2, err: errors.New("boom")}
	err := keepalive(context.Background(), p, time.Millisecond, time.Second)
	if err == nil {
		t.Fatal("keepalive returned nil, want an error after a ping fails")
	}
}

func TestKeepaliveReturnsNilOnContextCancel(t *testing.T) {
	p := &fakePinger{} // healthy: never fails
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	if err := keepalive(ctx, p, time.Millisecond, time.Second); err != nil {
		t.Fatalf("keepalive returned %v, want nil when ctx is cancelled", err)
	}
}

func TestKeepaliveTimesOutSlowPing(t *testing.T) {
	// A ping that never returns within the per-ping timeout means the link is
	// dead — keepalive must surface that as an error, not hang.
	p := &fakePinger{block: time.Hour}
	err := keepalive(context.Background(), p, time.Millisecond, 10*time.Millisecond)
	if err == nil {
		t.Fatal("keepalive returned nil, want an error when a ping exceeds the timeout")
	}
}
