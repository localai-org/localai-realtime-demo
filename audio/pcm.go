package audio

import (
	"encoding/binary"
	"math"
)

// bytesToS16Into decodes src (int16 LE bytes) into dst. Returns the number of
// int16 samples written. dst must have cap >= len(src)/2.
func bytesToS16Into(dst []int16, src []byte) int {
	n := len(src) / 2
	if n > len(dst) {
		n = len(dst)
	}
	for i := range n {
		dst[i] = int16(binary.LittleEndian.Uint16(src[i*2:]))
	}
	return n
}

// s16ToBytesInto encodes src into dst (int16 LE bytes). Returns bytes written.
func s16ToBytesInto(dst []byte, src []int16) int {
	n := len(src)
	if n*2 > len(dst) {
		n = len(dst) / 2
	}
	for i := range n {
		binary.LittleEndian.PutUint16(dst[i*2:], uint16(src[i]))
	}
	return n * 2
}

func bytesToS16(b []byte) []int16 {
	out := make([]int16, len(b)/2)
	bytesToS16Into(out, b)
	return out
}

func s16ToBytes(samples []int16) []byte {
	out := make([]byte, len(samples)*2)
	s16ToBytesInto(out, samples)
	return out
}

// resampleS16SamplesInto resamples src into dst with linear interpolation.
// Returns the number of samples written. dst cap must be at least
// ceil(len(src) * toRate / fromRate).
func resampleS16SamplesInto(dst, src []int16, fromRate, toRate int) int {
	if fromRate == toRate {
		return copy(dst, src)
	}
	if len(src) == 0 {
		return 0
	}
	ratio := float64(fromRate) / float64(toRate)
	newLen := int(float64(len(src)) / ratio)
	if newLen > len(dst) {
		newLen = len(dst)
	}
	for i := range newLen {
		srcPos := float64(i) * ratio
		srcIdx := int(srcPos)
		frac := srcPos - float64(srcIdx)
		if srcIdx >= len(src)-1 {
			dst[i] = src[len(src)-1]
			continue
		}
		dst[i] = int16(float64(src[srcIdx])*(1-frac) + float64(src[srcIdx+1])*frac)
	}
	return newLen
}

// resampleS16Into resamples src (int16 LE bytes) into dst using linear
// interpolation. Returns the number of bytes written.
func resampleS16Into(dst, src []byte, fromRate, toRate int) int {
	if fromRate == toRate {
		return copy(dst, src)
	}
	numSamples := len(src) / 2
	ratio := float64(fromRate) / float64(toRate)
	newNumSamples := int(float64(numSamples) / ratio)
	if newNumSamples*2 > len(dst) {
		newNumSamples = len(dst) / 2
	}
	for i := range newNumSamples {
		srcPos := float64(i) * ratio
		srcIdx := int(srcPos)
		frac := srcPos - float64(srcIdx)
		if srcIdx >= numSamples-1 {
			binary.LittleEndian.PutUint16(dst[i*2:], binary.LittleEndian.Uint16(src[(numSamples-1)*2:]))
			continue
		}
		sample1 := int16(binary.LittleEndian.Uint16(src[srcIdx*2:]))
		sample2 := int16(binary.LittleEndian.Uint16(src[(srcIdx+1)*2:]))
		interpolated := int16(float64(sample1)*(1-frac) + float64(sample2)*frac)
		binary.LittleEndian.PutUint16(dst[i*2:], uint16(interpolated))
	}
	return newNumSamples * 2
}

// rmsS16Samples returns the RMS of int16 samples.
func rmsS16Samples(samples []int16) float64 {
	if len(samples) == 0 {
		return 0
	}
	sum := 0.0
	for _, s := range samples {
		sum += float64(s) * float64(s)
	}
	return math.Sqrt(sum / float64(len(samples)))
}
