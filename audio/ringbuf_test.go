package audio

import "testing"

func TestInt16RingWriteRead(t *testing.T) {
	r := NewInt16Ring(8)
	r.Write([]int16{1, 2, 3})
	if r.Len() != 3 {
		t.Fatalf("len = %d, want 3", r.Len())
	}
	dst := make([]int16, 3)
	n := r.Read(dst)
	if n != 3 {
		t.Fatalf("read = %d, want 3", n)
	}
	for i, want := range []int16{1, 2, 3} {
		if dst[i] != want {
			t.Fatalf("dst[%d] = %d, want %d", i, dst[i], want)
		}
	}
	if r.Len() != 0 {
		t.Fatalf("len after read = %d, want 0", r.Len())
	}
}

func TestInt16RingUnderrunZeroFills(t *testing.T) {
	r := NewInt16Ring(8)
	r.Write([]int16{5, 6})
	dst := []int16{9, 9, 9, 9}
	n := r.Read(dst)
	if n != 2 {
		t.Fatalf("real samples = %d, want 2", n)
	}
	if dst[0] != 5 || dst[1] != 6 || dst[2] != 0 || dst[3] != 0 {
		t.Fatalf("dst = %v, want [5 6 0 0]", dst)
	}
}

func TestInt16RingOverrunDropsOldest(t *testing.T) {
	r := NewInt16Ring(4)
	r.Write([]int16{1, 2, 3})
	r.Write([]int16{4, 5}) // capacity 4, oldest (1) dropped
	if r.Len() != 4 {
		t.Fatalf("len = %d, want 4", r.Len())
	}
	dst := make([]int16, 4)
	r.Read(dst)
	for i, want := range []int16{2, 3, 4, 5} {
		if dst[i] != want {
			t.Fatalf("dst[%d] = %d, want %d", i, dst[i], want)
		}
	}
}

func TestInt16RingBatchLargerThanCap(t *testing.T) {
	r := NewInt16Ring(3)
	r.Write([]int16{1, 2, 3, 4, 5}) // keep only the last 3
	dst := make([]int16, 3)
	r.Read(dst)
	for i, want := range []int16{3, 4, 5} {
		if dst[i] != want {
			t.Fatalf("dst[%d] = %d, want %d", i, dst[i], want)
		}
	}
}
