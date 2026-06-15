package realtime

import (
	"context"
	"fmt"
	"time"
)

// Pinger is the subset of the connection used for liveness checks. *openairt.Conn
// satisfies it via its WebSocket Ping (ping frame + pong wait).
type Pinger interface {
	Ping(ctx context.Context) error
}

// keepalive pings p every interval; each ping must return within timeout. It is
// how a silently-dropped network (interface down, no TCP FIN/RST) is detected:
// a blocked ReadMessage never errors on its own, but a ping with no pong within
// timeout does. keepalive returns nil when ctx is cancelled (the session is
// ending for another reason), or an error describing the first failed/timed-out
// ping so the caller can end the session and let the Supervisor fail over.
func keepalive(ctx context.Context, p Pinger, interval, timeout time.Duration) error {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			pingCtx, cancel := context.WithTimeout(ctx, timeout)
			err := p.Ping(pingCtx)
			cancel()
			if ctx.Err() != nil {
				// Parent cancelled while pinging: a clean end, not a dead link.
				return nil
			}
			if err != nil {
				return fmt.Errorf("keepalive ping failed: %w", err)
			}
		}
	}
}
