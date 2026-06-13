package audio

import "testing"

func TestPlayerWriteThenFill(t *testing.T) {
	p := NewPlayer()
	p.Write([]byte{1, 2, 3, 4})

	out := make([]byte, 8)
	n := p.fill(out)
	if n != 4 {
		t.Fatalf("fill returned %d, want 4", n)
	}
	want := []byte{1, 2, 3, 4, 0, 0, 0, 0}
	for i := range want {
		if out[i] != want[i] {
			t.Fatalf("out = %v, want %v", out[:8], want)
		}
	}
}

func TestPlayerFillPartialDrain(t *testing.T) {
	p := NewPlayer()
	p.Write([]byte{1, 2, 3, 4, 5, 6})

	out := make([]byte, 4)
	if n := p.fill(out); n != 4 {
		t.Fatalf("first fill returned %d, want 4", n)
	}
	// Remaining two bytes drain on the next read.
	if n := p.fill(out); n != 2 {
		t.Fatalf("second fill returned %d, want 2", n)
	}
	if n := p.fill(out); n != 0 {
		t.Fatalf("third fill returned %d, want 0 (drained)", n)
	}
}

func TestPlayerFlushDropsPending(t *testing.T) {
	p := NewPlayer()
	p.Write(make([]byte, 100))

	if dropped := p.Flush(); dropped != 100 {
		t.Fatalf("Flush dropped %d, want 100", dropped)
	}
	// After flush the buffer is empty.
	if dropped := p.Flush(); dropped != 0 {
		t.Fatalf("second Flush dropped %d, want 0", dropped)
	}
	out := make([]byte, 8)
	if n := p.fill(out); n != 0 {
		t.Fatalf("fill after flush returned %d, want 0", n)
	}
}
