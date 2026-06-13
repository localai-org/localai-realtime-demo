package audio

import "sync"

// Player buffers PCM16 playback audio between the realtime client (producer,
// via Write) and the audio device callback (consumer, via fill). The server
// streams a whole response in a burst, so the buffer can hold seconds of audio
// that drain at the real-time device rate.
//
// Flush drops all pending audio so the assistant goes silent immediately when
// the user barges in — by the time speech is detected the server has usually
// finished sending, so cancelling server-side alone would not stop the locally
// buffered TTS.
type Player struct {
	mu  sync.Mutex
	buf []byte
}

// NewPlayer returns an empty Player.
func NewPlayer() *Player { return &Player{} }

// Write appends PCM bytes to the playback buffer.
func (p *Player) Write(b []byte) {
	if len(b) == 0 {
		return
	}
	p.mu.Lock()
	p.buf = append(p.buf, b...)
	p.mu.Unlock()
}

// fill copies pending audio into out and returns the number of bytes copied.
// The remainder of out is left untouched for the caller to pad with silence.
func (p *Player) fill(out []byte) int {
	p.mu.Lock()
	n := copy(out, p.buf)
	p.buf = p.buf[n:]
	if len(p.buf) == 0 {
		p.buf = nil
	}
	p.mu.Unlock()
	return n
}

// Flush drops all pending playback audio and returns the number of bytes
// dropped. Called on barge-in so locally buffered TTS stops immediately.
func (p *Player) Flush() int {
	p.mu.Lock()
	n := len(p.buf)
	p.buf = nil
	p.mu.Unlock()
	return n
}
