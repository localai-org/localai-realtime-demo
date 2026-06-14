package audio

import (
	"fmt"
	"strings"

	"github.com/gen2brain/malgo"
)

// DeviceSelection names the capture and/or playback devices to open instead of
// the system defaults. Each field is a case-insensitive substring matched
// against the device names reported by the audio backend (see ListDevices); an
// empty field means "use the default device". Selecting devices explicitly
// avoids depending on ALSA's `default` PCM, which can be fragile for duplex.
type DeviceSelection struct {
	Capture  string
	Playback string
}

// DeviceName is a discovered audio device, returned by ListDevices.
type DeviceName struct {
	Name      string
	IsDefault bool
}

// pickDevice returns the index into names whose entry matches query, or -1 when
// query is empty (meaning "use the default"). Matching is case-insensitive: an
// exact name wins; otherwise a unique substring match is used. It errors when
// nothing matches or a substring is ambiguous, listing the candidates so the
// caller can tell the user what to pick.
func pickDevice(names []string, query string) (int, error) {
	if query == "" {
		return -1, nil
	}
	q := strings.ToLower(strings.TrimSpace(query))

	for i, n := range names {
		if strings.ToLower(strings.TrimSpace(n)) == q {
			return i, nil
		}
	}

	var matches []int
	for i, n := range names {
		if strings.Contains(strings.ToLower(n), q) {
			matches = append(matches, i)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return -1, fmt.Errorf("no audio device matches %q; available: %s", query, strings.Join(names, " | "))
	default:
		var c []string
		for _, i := range matches {
			c = append(c, names[i])
		}
		return -1, fmt.Errorf("audio device %q is ambiguous, matches: %s", query, strings.Join(c, " | "))
	}
}

// Backends maps a human backend name to the malgo backend list passed to
// InitContext. An empty/unknown name returns nil, which lets miniaudio pick
// automatically. Forcing a backend (e.g. "alsa") is the escape hatch when the
// auto-selected one misbehaves — notably miniaudio's PulseAudio playback, which
// can leave the device in a permanent XRUN on some PipeWire setups.
func Backends(name string) []malgo.Backend {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "alsa":
		return []malgo.Backend{malgo.BackendAlsa}
	case "pulse", "pulseaudio":
		return []malgo.Backend{malgo.BackendPulseaudio}
	case "jack":
		return []malgo.Backend{malgo.BackendJack}
	default:
		return nil
	}
}

// ListDevices enumerates the capture and playback devices the backend can see.
// It is used by the -list-audio-devices flag so users can discover the exact
// names to pass for device selection. backend optionally forces a specific
// audio backend (see Backends); empty means auto.
func ListDevices(backend string) (capture, playback []DeviceName, err error) {
	mctx, err := malgo.InitContext(Backends(backend), malgo.ContextConfig{}, func(string) {})
	if err != nil {
		return nil, nil, fmt.Errorf("init audio context: %w", err)
	}
	defer func() {
		_ = mctx.Uninit()
		mctx.Free()
	}()

	caps, err := mctx.Devices(malgo.Capture)
	if err != nil {
		return nil, nil, fmt.Errorf("enumerate capture devices: %w", err)
	}
	plays, err := mctx.Devices(malgo.Playback)
	if err != nil {
		return nil, nil, fmt.Errorf("enumerate playback devices: %w", err)
	}
	for i := range caps {
		capture = append(capture, DeviceName{Name: caps[i].Name(), IsDefault: caps[i].IsDefault != 0})
	}
	for i := range plays {
		playback = append(playback, DeviceName{Name: plays[i].Name(), IsDefault: plays[i].IsDefault != 0})
	}
	return capture, playback, nil
}

// resolveDevices enumerates devices and returns the chosen capture/playback
// DeviceInfo for sel (nil entry = use the default). The returned pointers must
// outlive the malgo.InitDevice call that consumes their IDs.
func resolveDevices(mctx malgo.Context, sel *DeviceSelection) (capDev, playDev *malgo.DeviceInfo, err error) {
	if sel == nil || (sel.Capture == "" && sel.Playback == "") {
		return nil, nil, nil
	}
	if sel.Capture != "" {
		infos, e := mctx.Devices(malgo.Capture)
		if e != nil {
			return nil, nil, fmt.Errorf("enumerate capture devices: %w", e)
		}
		i, e := pickDevice(deviceNames(infos), sel.Capture)
		if e != nil {
			return nil, nil, fmt.Errorf("capture: %w", e)
		}
		capDev = &infos[i]
	}
	if sel.Playback != "" {
		infos, e := mctx.Devices(malgo.Playback)
		if e != nil {
			return nil, nil, fmt.Errorf("enumerate playback devices: %w", e)
		}
		i, e := pickDevice(deviceNames(infos), sel.Playback)
		if e != nil {
			return nil, nil, fmt.Errorf("playback: %w", e)
		}
		playDev = &infos[i]
	}
	return capDev, playDev, nil
}

func deviceNames(infos []malgo.DeviceInfo) []string {
	names := make([]string, len(infos))
	for i := range infos {
		names[i] = infos[i].Name()
	}
	return names
}
