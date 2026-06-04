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
