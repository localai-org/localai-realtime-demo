package audio

import (
	"encoding/binary"
	"math"
	"time"
)

// ToneSweep generates a linear-frequency sine sweep from startHz to endHz over
// dur, returned as little-endian PCM16 mono at sampleRate. It is used as an
// audible cue when the assistant switches between realtime endpoints: an
// ascending sweep for "recovered toward primary", a descending one for "dropped
// to fallback". A short fade in/out avoids clicks. Returns nil for non-positive
// durations.
func ToneSweep(sampleRate int, startHz, endHz float64, dur time.Duration) []byte {
	n := int(float64(sampleRate) * dur.Seconds())
	if n <= 0 {
		return nil
	}
	const amp = 0.3 // keep the cue well below full scale
	out := make([]byte, n*2)
	phase := 0.0
	fade := n / 10
	for i := range n {
		frac := float64(i) / float64(n)
		freq := startHz + (endHz-startHz)*frac
		phase += 2 * math.Pi * freq / float64(sampleRate)

		env := 1.0
		if fade > 0 {
			if i < fade {
				env = float64(i) / float64(fade)
			} else if i >= n-fade {
				env = float64(n-1-i) / float64(fade)
			}
		}

		s := int16(amp * env * math.Sin(phase) * math.MaxInt16)
		binary.LittleEndian.PutUint16(out[2*i:], uint16(s))
	}
	return out
}
