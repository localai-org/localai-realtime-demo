package audio

import "testing"

// fakeEngine is an identity LocalVQE: it copies mic -> out, ignoring ref.
type fakeEngine struct {
	rate int
	hop  int
}

func (f fakeEngine) SampleRate() int { return f.rate }
func (f fakeEngine) HopLength() int  { return f.hop }
func (f fakeEngine) ProcessFrameS16Into(mic, ref, out []int16) error {
	copy(out, mic)
	return nil
}

func TestProcessorBuffersUntilHop(t *testing.T) {
	eng := fakeEngine{rate: 16000, hop: 256}
	p := NewLocalVQEProcessor(eng, 16000, 4096)

	// 100 samples < hop(256): nothing emitted yet.
	rec := make([]byte, 200)
	play := make([]byte, 200)
	out := make([]byte, 4096)
	if n := p.Process(rec, play, out); n != 0 {
		t.Fatalf("Process returned %d bytes, want 0 while buffering", n)
	}
}

func TestProcessorIdentityNoResample(t *testing.T) {
	eng := fakeEngine{rate: 16000, hop: 256}
	p := NewLocalVQEProcessor(eng, 16000, 4096)

	// One full hop of a ramp; deviceRate == modelRate so output == input.
	in := make([]int16, 256)
	for i := range in {
		in[i] = int16(i)
	}
	rec := s16ToBytes(in)
	play := make([]byte, len(rec))
	out := make([]byte, 4096)

	n := p.Process(rec, play, out)
	if n != len(rec) {
		t.Fatalf("Process returned %d bytes, want %d", n, len(rec))
	}
	got := bytesToS16(out[:n])
	for i := range in {
		if got[i] != in[i] {
			t.Fatalf("sample %d = %d, want %d", i, got[i], in[i])
		}
	}
}

func TestProcessorResamples24kTo16k(t *testing.T) {
	eng := fakeEngine{rate: 16000, hop: 256}
	p := NewLocalVQEProcessor(eng, 24000, 4096)

	// 480 device samples (20 ms @ 24k) -> 320 @ 16k >= one hop(256).
	rec := make([]byte, 480*2)
	play := make([]byte, 480*2)
	out := make([]byte, 4096)

	n := p.Process(rec, play, out)
	if n <= 0 {
		t.Fatalf("Process returned %d bytes, want > 0 after one hop", n)
	}
}
