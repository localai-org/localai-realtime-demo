package audio

import (
	"context"
	"fmt"
	"sync"

	"github.com/gen2brain/malgo"
)

// AECOptions enables acoustic echo cancellation in Duplex. When non-nil and
// Engine is set, captured mic audio is cleaned against the speaker reference
// (the PCM we send to the device, i.e. playback-ref mode) before it reaches
// micOut. Neural inference runs on a worker goroutine, never in the audio
// callback. When nil, Duplex passes mic audio straight through (legacy
// behavior).
type AECOptions struct {
	// Engine is the LocalVQE binding (16 kHz mono). Required to enable AEC.
	Engine LocalVQEEngine
	// DelayMs is the reference delay compensating the speaker->air->mic
	// acoustic path. The ref ring is seeded with this many ms of silence so
	// the reference lags the mic. Default 50 when <= 0.
	DelayMs int
}

// Duplex opens the default capture+playback device as PCM16 mono at the given
// sample rate. Captured microphone frames are pushed to micOut (dropped if the
// channel is full); PCM bytes received on playIn are played through the
// speaker. When aec is non-nil and aec.Engine is set, mic audio is echo-
// cancelled against the speaker output before reaching micOut. It blocks until
// ctx is cancelled or the device errors.
func Duplex(ctx context.Context, sampleRate int, micOut chan<- []byte, playIn <-chan []byte, aec *AECOptions) error {
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

	// AEC wiring (playback-ref + off-thread worker). Nil-safe.
	aecEnabled := aec != nil && aec.Engine != nil
	var micRing, refRing *Int16Ring
	if aecEnabled {
		// ~1s rings at device rate decouple the callback from the worker.
		micRing = NewInt16Ring(sampleRate)
		refRing = NewInt16Ring(sampleRate)

		// Seed the reference with DelayMs of silence so it lags the mic.
		delayMs := aec.DelayMs
		if delayMs <= 0 {
			delayMs = 50
		}
		seed := make([]int16, delayMs*sampleRate/1000)
		refRing.Write(seed)

		// maxBytesPerCall matches the worker's 20ms batch (deviceRate/50).
		batchBytes := (sampleRate / 50) * 2
		proc := NewLocalVQEProcessor(aec.Engine, sampleRate, batchBytes)
		NewAECWorker(ctx, proc, micRing, refRing, sampleRate, sampleRate, chanWriter{ch: micOut}, nil)
	}

	// Reusable scratch for converting callback buffers to int16 (AEC path).
	var micScratch, refScratch []int16

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

		if len(in) == 0 {
			return
		}

		if aecEnabled {
			// Enqueue mic + reference (the exact bytes we just wrote to the
			// speaker) for the worker. The worker cleans and forwards to
			// micOut. Int16Ring.Write copies, so reusing scratch is safe.
			nm := len(in) / 2
			if cap(micScratch) < nm {
				micScratch = make([]int16, nm)
			}
			micScratch = micScratch[:nm]
			bytesToS16Into(micScratch, in)
			micRing.Write(micScratch)

			nr := len(out) / 2
			if cap(refScratch) < nr {
				refScratch = make([]int16, nr)
			}
			refScratch = refScratch[:nr]
			bytesToS16Into(refScratch, out)
			refRing.Write(refScratch)
			return
		}

		// Passthrough: copy mic frames and hand off without blocking. Dropping
		// an interior mic frame splices discontinuous PCM, which degrades
		// server-side VAD/transcription, so size micOut to make drops rare.
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
