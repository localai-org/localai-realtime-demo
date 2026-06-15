package audio

import (
	"testing"
	"time"
)

func TestToneSweepLength(t *testing.T) {
	b := ToneSweep(24000, 440, 660, 200*time.Millisecond)
	// 24000 Hz * 0.2 s = 4800 samples, 2 bytes/sample (PCM16 mono).
	want := 4800 * 2
	if len(b) != want {
		t.Fatalf("len = %d, want %d", len(b), want)
	}
}

func TestToneSweepNonSilent(t *testing.T) {
	b := ToneSweep(24000, 440, 660, 200*time.Millisecond)
	nonzero := false
	for _, by := range b {
		if by != 0 {
			nonzero = true
			break
		}
	}
	if !nonzero {
		t.Fatal("tone is entirely silent")
	}
}

func TestToneSweepZeroDurationIsNil(t *testing.T) {
	if b := ToneSweep(24000, 440, 660, 0); b != nil {
		t.Fatalf("zero duration: got %d bytes, want nil", len(b))
	}
}

func TestChimeLength(t *testing.T) {
	// Two discrete notes of 80ms each at 24kHz = 2 * 1920 samples, 2 bytes each.
	b := Chime(24000, []float64{660, 990}, 80*time.Millisecond)
	perNote := int(24000 * 0.08)
	want := 2 * perNote * 2
	if len(b) != want {
		t.Fatalf("len = %d, want %d", len(b), want)
	}
}

func TestChimeNonSilent(t *testing.T) {
	b := Chime(24000, []float64{660, 990}, 80*time.Millisecond)
	for _, by := range b {
		if by != 0 {
			return
		}
	}
	t.Fatal("chime is entirely silent")
}

func TestChimeNoNotesIsNil(t *testing.T) {
	if b := Chime(24000, nil, 80*time.Millisecond); b != nil {
		t.Fatalf("no notes: got %d bytes, want nil", len(b))
	}
}

func TestChimeZeroDurationIsNil(t *testing.T) {
	if b := Chime(24000, []float64{660, 990}, 0); b != nil {
		t.Fatalf("zero duration: got %d bytes, want nil", len(b))
	}
}
