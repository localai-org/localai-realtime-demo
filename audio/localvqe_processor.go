package audio

import (
	"log"
	"math"
)

// LocalVQEEngine is the subset of the LocalVQE binding the processor needs.
// It is satisfied by *localvqe.LocalVQE.
type LocalVQEEngine interface {
	ProcessFrameS16Into(mic, ref, out []int16) error
	SampleRate() int
	HopLength() int
}

// localvqeProcessor streams audio through LocalVQE one hop at a time, operating
// entirely on preallocated scratch after construction. LocalVQE runs at its
// native SampleRate (16 kHz); device audio at a different rate is resampled
// internally.
type localvqeProcessor struct {
	engine     LocalVQEEngine
	deviceRate int
	modelRate  int
	hopLength  int
	maxIn      int

	micDevice []int16
	refDevice []int16
	micModel  []int16
	refModel  []int16
	micBuf    []int16
	refBuf    []int16
	micBufLen int
	refBufLen int
	frameOut  []int16
	outModel  []int16
	outDevice []int16

	diagInSum  float64
	diagOutSum float64
	diagRefSum float64
	diagCount  int
}

// NewLocalVQEProcessor creates a streaming AEC processor with all scratch
// preallocated. maxBytesPerCall is an upper bound on len(rec)/len(play) in a
// single Process call; callers must stay at or below this.
func NewLocalVQEProcessor(engine LocalVQEEngine, deviceRate, maxBytesPerCall int) *localvqeProcessor {
	hop := engine.HopLength()
	modelRate := engine.SampleRate()

	maxDeviceSamples := maxBytesPerCall / 2
	if maxDeviceSamples < 1 {
		maxDeviceSamples = 1
	}
	maxModelSamples := (maxDeviceSamples*modelRate + deviceRate - 1) / deviceRate
	if maxModelSamples < 1 {
		maxModelSamples = 1
	}
	accum := maxModelSamples + hop

	return &localvqeProcessor{
		engine:     engine,
		deviceRate: deviceRate,
		modelRate:  modelRate,
		hopLength:  hop,
		maxIn:      maxDeviceSamples,

		micDevice: make([]int16, maxDeviceSamples),
		refDevice: make([]int16, maxDeviceSamples),
		micModel:  make([]int16, maxModelSamples),
		refModel:  make([]int16, maxModelSamples),
		micBuf:    make([]int16, accum),
		refBuf:    make([]int16, accum),
		frameOut:  make([]int16, hop),
		outModel:  make([]int16, accum),
		outDevice: make([]int16, (accum*deviceRate+modelRate-1)/modelRate+1),
	}
}

// Process runs AEC on rec/play byte slices (int16 LE at deviceRate), writing
// cleaned samples to out. Returns bytes written (0 if still buffering or on
// overflow). Steady-state allocation: zero.
func (p *localvqeProcessor) Process(rec, play, out []byte) int {
	if len(rec) == 0 || len(play) == 0 {
		return 0
	}
	if len(rec) > p.maxIn*2 || len(play) > p.maxIn*2 {
		log.Printf("localvqe: Process rec=%d play=%d exceeds max=%d bytes", len(rec), len(play), p.maxIn*2)
		return 0
	}

	nIn := len(rec) / 2
	micDev := p.micDevice[:nIn]
	refDev := p.refDevice[:nIn]
	bytesToS16Into(micDev, rec)
	bytesToS16Into(refDev, play)

	var micM, refM []int16
	if p.deviceRate != p.modelRate {
		nM := resampleS16SamplesInto(p.micModel[:cap(p.micModel)], micDev, p.deviceRate, p.modelRate)
		_ = resampleS16SamplesInto(p.refModel[:cap(p.refModel)], refDev, p.deviceRate, p.modelRate)
		micM = p.micModel[:nM]
		refM = p.refModel[:nM]
	} else {
		micM = micDev
		refM = refDev
	}

	if p.micBufLen+len(micM) > len(p.micBuf) || p.refBufLen+len(refM) > len(p.refBuf) {
		log.Printf("localvqe: accum overflow (bufLen=%d new=%d cap=%d)", p.micBufLen, len(micM), len(p.micBuf))
		return 0
	}
	copy(p.micBuf[p.micBufLen:], micM)
	copy(p.refBuf[p.refBufLen:], refM)
	p.micBufLen += len(micM)
	p.refBufLen += len(refM)

	outModelLen := 0
	for p.micBufLen >= p.hopLength && p.refBufLen >= p.hopLength {
		if err := p.engine.ProcessFrameS16Into(p.micBuf[:p.hopLength], p.refBuf[:p.hopLength], p.frameOut); err != nil {
			log.Printf("localvqe: ProcessFrameS16Into error: %v", err)
			copy(p.frameOut, p.micBuf[:p.hopLength])
		}

		p.diagInSum += rmsS16Samples(p.micBuf[:p.hopLength])
		p.diagRefSum += rmsS16Samples(p.refBuf[:p.hopLength])
		p.diagOutSum += rmsS16Samples(p.frameOut)
		p.diagCount++

		if outModelLen+p.hopLength > len(p.outModel) {
			log.Printf("localvqe: outModel overflow")
			break
		}
		copy(p.outModel[outModelLen:], p.frameOut)
		outModelLen += p.hopLength

		remMic := p.micBufLen - p.hopLength
		remRef := p.refBufLen - p.hopLength
		copy(p.micBuf, p.micBuf[p.hopLength:p.micBufLen])
		copy(p.refBuf, p.refBuf[p.hopLength:p.refBufLen])
		p.micBufLen = remMic
		p.refBufLen = remRef
	}

	p.logDiag()

	if outModelLen == 0 {
		return 0
	}

	var outD []int16
	if p.deviceRate != p.modelRate {
		nD := resampleS16SamplesInto(p.outDevice[:cap(p.outDevice)], p.outModel[:outModelLen], p.modelRate, p.deviceRate)
		outD = p.outDevice[:nD]
	} else {
		outD = p.outModel[:outModelLen]
	}

	nBytes := len(outD) * 2
	if nBytes > len(out) {
		log.Printf("localvqe: out buf too small: need %d have %d", nBytes, len(out))
		nBytes = len(out) &^ 1
	}
	s16ToBytesInto(out[:nBytes], outD[:nBytes/2])
	return nBytes
}

func (p *localvqeProcessor) logDiag() {
	if p.diagCount == 0 || p.diagCount%500 != 0 {
		return
	}
	cnt := float64(p.diagCount)
	avgIn := p.diagInSum / cnt
	avgRef := p.diagRefSum / cnt
	avgOut := p.diagOutSum / cnt
	reductionDB := math.NaN()
	if avgIn > 0 {
		reductionDB = 20 * math.Log10(avgOut/avgIn)
	}
	log.Printf("LocalVQE: avgIn=%.0f avgRef=%.0f avgOut=%.0f reduction=%.1fdB hops=%d", avgIn, avgRef, avgOut, reductionDB, p.diagCount)
	p.diagInSum = 0
	p.diagRefSum = 0
	p.diagOutSum = 0
	p.diagCount = 0
}
