package audio

import "testing"

func TestBytesS16RoundTrip(t *testing.T) {
	src := []int16{0, 1, -1, 32767, -32768, 1234}
	b := s16ToBytes(src)
	if len(b) != len(src)*2 {
		t.Fatalf("len = %d, want %d", len(b), len(src)*2)
	}
	got := bytesToS16(b)
	if len(got) != len(src) {
		t.Fatalf("decoded len = %d, want %d", len(got), len(src))
	}
	for i := range src {
		if got[i] != src[i] {
			t.Fatalf("sample %d = %d, want %d", i, got[i], src[i])
		}
	}
}

func TestResampleS16SamplesIdentity(t *testing.T) {
	src := []int16{10, 20, 30, 40}
	dst := make([]int16, len(src))
	n := resampleS16SamplesInto(dst, src, 16000, 16000)
	if n != len(src) {
		t.Fatalf("n = %d, want %d", n, len(src))
	}
	for i := range src {
		if dst[i] != src[i] {
			t.Fatalf("sample %d = %d, want %d", i, dst[i], src[i])
		}
	}
}

func TestResampleS16SamplesDownsample(t *testing.T) {
	// 24k -> 16k is a 2:3 ratio; 480 samples in -> 320 out.
	src := make([]int16, 480)
	dst := make([]int16, 480)
	n := resampleS16SamplesInto(dst, src, 24000, 16000)
	if n != 320 {
		t.Fatalf("n = %d, want 320", n)
	}
}

func TestRMSS16Samples(t *testing.T) {
	if got := rmsS16Samples(nil); got != 0 {
		t.Fatalf("rms(nil) = %v, want 0", got)
	}
	if got := rmsS16Samples([]int16{3, 4}); got < 3.5 || got > 3.6 {
		t.Fatalf("rms({3,4}) = %v, want ~3.54", got)
	}
}
