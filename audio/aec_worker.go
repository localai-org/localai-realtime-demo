package audio

import (
	"context"
	"io"
	"log"
	"time"
)

// AudioProcessor cleans captured audio using a playback reference. Process
// takes rec (mic) and play (speaker reference) int16 LE PCM at the device rate
// plus a caller-owned out buffer, writes cleaned samples into out, and returns
// the number of bytes written (0 if still buffering). Implementations must not
// allocate in the steady state.
type AudioProcessor interface {
	Process(rec, play, out []byte) int
}

// AECWorkerOpts configures NewAECWorker.
type AECWorkerOpts struct {
	// Poll interval for draining the mic/ref rings. Defaults to 5ms.
	TickInterval time.Duration
}

// AECWorker drains mic+ref rings, runs processor, and writes cleaned samples
// (resampled to outputRate when it differs from deviceRate) to out. All scratch
// is preallocated so the run loop is zero-alloc in the steady state.
type AECWorker struct {
	ctx          context.Context
	processor    AudioProcessor
	micRing      *Int16Ring
	refRing      *Int16Ring
	deviceRate   int
	outputRate   int
	out          io.Writer
	opts         *AECWorkerOpts
	batchSamples int

	micInt16    []int16
	refInt16    []int16
	micBytes    []byte
	refBytes    []byte
	cleanedBuf  []byte
	resampleOut []byte

	done chan struct{}
}

// NewAECWorker spawns a goroutine that reads mic/ref samples from the rings,
// runs processor.Process, and writes cleaned bytes to out. deviceRate is the
// rate carried by the rings; outputRate is the rate expected by out. The worker
// exits when ctx is done.
func NewAECWorker(ctx context.Context, processor AudioProcessor, micRing, refRing *Int16Ring, deviceRate, outputRate int, out io.Writer, opts *AECWorkerOpts) *AECWorker {
	batchSamples := deviceRate / 50
	if batchSamples < 1 {
		batchSamples = 1
	}

	cleanedBytes := batchSamples * 4
	resampleOutBytes := (cleanedBytes*outputRate+deviceRate-1)/deviceRate + 2
	if resampleOutBytes < cleanedBytes {
		resampleOutBytes = cleanedBytes
	}

	w := &AECWorker{
		ctx:          ctx,
		processor:    processor,
		micRing:      micRing,
		refRing:      refRing,
		deviceRate:   deviceRate,
		outputRate:   outputRate,
		out:          out,
		opts:         opts,
		batchSamples: batchSamples,

		micInt16:    make([]int16, batchSamples),
		refInt16:    make([]int16, batchSamples),
		micBytes:    make([]byte, batchSamples*2),
		refBytes:    make([]byte, batchSamples*2),
		cleanedBuf:  make([]byte, cleanedBytes),
		resampleOut: make([]byte, resampleOutBytes),

		done: make(chan struct{}),
	}
	go w.run()
	return w
}

// Done returns a channel closed when the worker's run loop exits.
func (w *AECWorker) Done() <-chan struct{} { return w.done }

func (w *AECWorker) run() {
	defer close(w.done)

	interval := 5 * time.Millisecond
	if w.opts != nil && w.opts.TickInterval > 0 {
		interval = w.opts.TickInterval
	}
	tick := time.NewTicker(interval)
	defer tick.Stop()

	for {
		select {
		case <-w.ctx.Done():
			return
		case <-tick.C:
			w.drain()
		}
	}
}

func (w *AECWorker) drain() {
	for w.micRing.Len() >= w.batchSamples && w.refRing.Len() >= w.batchSamples {
		w.micRing.Read(w.micInt16)
		w.refRing.Read(w.refInt16)
		s16ToBytesInto(w.micBytes, w.micInt16)
		s16ToBytesInto(w.refBytes, w.refInt16)

		n := w.processor.Process(w.micBytes, w.refBytes, w.cleanedBuf)
		if n == 0 {
			continue
		}
		cleaned := w.cleanedBuf[:n]

		if w.outputRate != 0 && w.outputRate != w.deviceRate {
			need := n*w.outputRate/w.deviceRate + 2
			if need > cap(w.resampleOut) {
				w.resampleOut = make([]byte, need)
			}
			w.resampleOut = w.resampleOut[:cap(w.resampleOut)]
			m := resampleS16Into(w.resampleOut, cleaned, w.deviceRate, w.outputRate)
			cleaned = w.resampleOut[:m]
		}

		if _, err := w.out.Write(cleaned); err != nil {
			log.Printf("AECWorker: write error: %v", err)
			return
		}
	}
}

// chanWriter is an io.Writer that copies each write and forwards it to a
// []byte channel, dropping on a full channel (the same non-blocking semantics
// the duplex capture path uses). Used as the AECWorker sink to deliver cleaned
// mic audio onto the existing micOut channel.
type chanWriter struct {
	ch chan<- []byte
}

func (w chanWriter) Write(p []byte) (int, error) {
	b := make([]byte, len(p))
	copy(b, p)
	select {
	case w.ch <- b:
	default:
	}
	return len(p), nil
}
