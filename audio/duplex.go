package audio

import (
	"context"
	"fmt"
	"log"

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

// Duplex opens separate capture and playback devices as PCM16 mono at the given
// sample rate. Two independent devices (rather than one duplex device) work
// reliably across backends — notably PulseAudio/PipeWire, where the mic and
// speaker are distinct nodes and a single duplex device may capture nothing.
// Captured microphone frames are pushed to micOut (dropped if the channel is
// full); audio written to player is played through the speaker. When aec is
// non-nil and aec.Engine is set, mic audio is echo-cancelled against the speaker
// output before reaching micOut. It blocks until ctx is cancelled or a device
// errors.
//
// sel optionally pins the capture/playback devices (nil = system defaults).
// When debug is set, the captured mic level is logged about once a second so
// you can tell whether real audio is reaching the app.
func Duplex(ctx context.Context, sampleRate int, micOut chan<- []byte, player *Player, aec *AECOptions, sel *DeviceSelection, debug bool) error {
	malgoCtx, err := malgo.InitContext(nil, malgo.ContextConfig{}, func(string) {})
	if err != nil {
		return fmt.Errorf("init audio context: %w", err)
	}
	defer func() {
		_ = malgoCtx.Uninit()
		malgoCtx.Free()
	}()

	// Resolve explicit devices before building the config; capDev/playDev must
	// stay alive until InitDevice consumes their IDs below.
	capDev, playDev, err := resolveDevices(malgoCtx.Context, sel)
	if err != nil {
		return err
	}

	// AEC wiring (playback-ref + off-thread worker). Nil-safe.
	aecEnabled := aec != nil && aec.Engine != nil
	var micRing, refRing *Int16Ring
	var worker *AECWorker
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
		worker = NewAECWorker(ctx, proc, micRing, refRing, sampleRate, sampleRate, chanWriter{ch: micOut}, nil)
	}

	// Reusable scratch for converting callback buffers to int16 (AEC path).
	// micScratch is touched only by the capture callback, refScratch only by the
	// playback callback, so the two devices' threads never share scratch.
	var micScratch, refScratch []int16

	// Capture-level debug: peak over a ~1s window, logged from the capture cb.
	var dbgSamples int
	var dbgPeak int16

	// Capture callback: mic frames -> AEC mic ring, or straight to micOut.
	onCapture := func(_, in []byte, _ uint32) {
		if len(in) == 0 {
			return
		}

		if debug {
			for i := 0; i+1 < len(in); i += 2 {
				s := int16(in[i]) | int16(in[i+1])<<8
				if s < 0 {
					s = -s
				}
				if s > dbgPeak {
					dbgPeak = s
				}
			}
			dbgSamples += len(in) / 2
			if dbgSamples >= sampleRate {
				log.Printf("audio: capture active, peak=%d (%.1f%%) over %d samples",
					dbgPeak, float64(dbgPeak)/327.67, dbgSamples)
				dbgSamples, dbgPeak = 0, 0
			}
		}

		if aecEnabled {
			// Int16Ring.Write copies, so reusing scratch is safe.
			nm := len(in) / 2
			if cap(micScratch) < nm {
				micScratch = make([]int16, nm)
			}
			micScratch = micScratch[:nm]
			bytesToS16Into(micScratch, in)
			micRing.Write(micScratch)
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

	// Playback callback: fill the speaker from the player (silence on underrun);
	// feed the exact output as the AEC reference.
	onPlayback := func(out, _ []byte, _ uint32) {
		n := player.fill(out)
		for i := n; i < len(out); i++ {
			out[i] = 0
		}
		if aecEnabled {
			nr := len(out) / 2
			if cap(refScratch) < nr {
				refScratch = make([]int16, nr)
			}
			refScratch = refScratch[:nr]
			bytesToS16Into(refScratch, out)
			refRing.Write(refScratch)
		}
	}

	// Capture device.
	capCfg := malgo.DefaultDeviceConfig(malgo.Capture)
	capCfg.Capture.Format = malgo.FormatS16
	capCfg.Capture.Channels = 1
	capCfg.SampleRate = uint32(sampleRate)
	capCfg.Alsa.NoMMap = 1
	if capDev != nil {
		capCfg.Capture.DeviceID = capDev.ID.Pointer()
		log.Printf("audio: capture device = %q", capDev.Name())
	}
	capDevice, err := malgo.InitDevice(malgoCtx.Context, capCfg, malgo.DeviceCallbacks{Data: onCapture})
	if err != nil {
		return fmt.Errorf("init capture device: %w", err)
	}
	defer capDevice.Uninit()

	// Playback device.
	playCfg := malgo.DefaultDeviceConfig(malgo.Playback)
	playCfg.Playback.Format = malgo.FormatS16
	playCfg.Playback.Channels = 1
	playCfg.SampleRate = uint32(sampleRate)
	playCfg.Alsa.NoMMap = 1
	if playDev != nil {
		playCfg.Playback.DeviceID = playDev.ID.Pointer()
		log.Printf("audio: playback device = %q", playDev.Name())
	}
	playDevice, err := malgo.InitDevice(malgoCtx.Context, playCfg, malgo.DeviceCallbacks{Data: onPlayback})
	if err != nil {
		return fmt.Errorf("init playback device: %w", err)
	}
	defer playDevice.Uninit()

	if err := capDevice.Start(); err != nil {
		return fmt.Errorf("start capture device: %w", err)
	}
	if err := playDevice.Start(); err != nil {
		return fmt.Errorf("start playback device: %w", err)
	}

	<-ctx.Done()
	// Wait for the AEC worker to stop before returning so the caller can safely
	// free the LocalVQE engine without racing a mid-drain inference.
	if worker != nil {
		<-worker.Done()
	}
	return nil
}
