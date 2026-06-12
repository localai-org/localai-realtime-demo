package audio

import (
	"context"
	"sync"
	"testing"
	"time"
)

// collectWriter records everything written to it.
type collectWriter struct {
	mu  sync.Mutex
	buf []byte
}

func (w *collectWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = append(w.buf, p...)
	return len(p), nil
}

func (w *collectWriter) len() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.buf)
}

func TestAECWorkerDrainsAndWrites(t *testing.T) {
	eng := fakeEngine{rate: 16000, hop: 256}
	proc := NewLocalVQEProcessor(eng, 16000, 4096)
	mic := NewInt16Ring(16000)
	ref := NewInt16Ring(16000)
	out := &collectWriter{}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := NewAECWorker(ctx, proc, mic, ref, 16000, 16000, out, &AECWorkerOpts{TickInterval: time.Millisecond})

	// Push ~1 batch (deviceRate/50 = 320 samples) several times.
	batch := make([]int16, 320)
	for range 4 {
		mic.Write(batch)
		ref.Write(batch)
	}

	deadline := time.After(2 * time.Second)
	for out.len() == 0 {
		select {
		case <-deadline:
			t.Fatal("worker produced no output within timeout")
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}
	cancel()
	<-w.Done()
}
