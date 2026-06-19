// Package speaker is noto's local speaker-embedding provider: ECAPA-TDNN-512
// (WeSpeaker ONNX, 192-d) running in-process via onnxruntime. CPU-only, no GPU.
//
// fbank.go reimplements the WeSpeaker feature frontend in pure Go so the embedder
// needs no Python sidecar: 80-bin kaldi-compatible log-mel filterbank (25 ms / 10 ms,
// povey window, pre-emphasis, DC removal, power spectrum, kaldi mel scale, log) followed
// by per-utterance cepstral mean normalization (CMN). It is validated bit-close against
// torchaudio.compliance.kaldi.fbank (see tools/voiceprint/goval).
package speaker

import "math"

const (
	sampleRate  = 16000
	frameLength = 400 // 25 ms
	frameShift  = 160 // 10 ms
	numMel      = 80
	fftSize     = 512 // next pow2 >= frameLength
	preemphasis = 0.97
	melLowFreq  = 20.0
	melHighFreq = 8000.0 // Nyquist at 16 kHz
	logEpsilon  = 1.1920929e-07
)

var (
	poveyWindow = makePoveyWindow()
	melBanks    = makeMelBanks() // [numMel][fftSize/2+1]
)

func makePoveyWindow() []float64 {
	w := make([]float64, frameLength)
	for n := 0; n < frameLength; n++ {
		hann := 0.5 - 0.5*math.Cos(2*math.Pi*float64(n)/float64(frameLength-1))
		w[n] = math.Pow(hann, 0.85)
	}
	return w
}

func melScale(f float64) float64 { return 1127.0 * math.Log(1.0+f/700.0) }

// makeMelBanks mirrors torchaudio.compliance.kaldi.get_mel_banks: triangular filters
// equally spaced in the kaldi mel domain over [low, high], computed against the mel of
// each FFT bin frequency, with the Nyquist column left at zero.
func makeMelBanks() [][]float64 {
	numFftBins := fftSize / 2 // 256; bin 256 (Nyquist) stays 0
	melLow, melHigh := melScale(melLowFreq), melScale(melHighFreq)
	delta := (melHigh - melLow) / float64(numMel+1)
	fftBinWidth := float64(sampleRate) / float64(fftSize)
	banks := make([][]float64, numMel)
	for b := 0; b < numMel; b++ {
		left := melLow + float64(b)*delta
		center := melLow + float64(b+1)*delta
		right := melLow + float64(b+2)*delta
		row := make([]float64, fftSize/2+1)
		for k := 0; k < numFftBins; k++ {
			mel := melScale(fftBinWidth * float64(k))
			up := (mel - left) / (center - left)
			down := (right - mel) / (right - center)
			v := math.Min(up, down)
			if v > 0 {
				row[k] = v
			}
		}
		banks[b] = row
	}
	return banks
}

// Fbank computes the WeSpeaker 80-bin log-mel features (T x 80) with CMN applied,
// for a mono 16 kHz waveform. Returns nil if the clip is shorter than one frame.
func Fbank(wav []float32) [][]float32 {
	if len(wav) < frameLength {
		return nil
	}
	numFrames := (len(wav)-frameLength)/frameShift + 1
	feats := make([][]float32, numFrames)
	re := make([]float64, fftSize)
	im := make([]float64, fftSize)
	colMean := make([]float64, numMel)
	for m := 0; m < numFrames; m++ {
		off := m * frameShift
		// copy frame + DC removal
		var mean float64
		for i := 0; i < frameLength; i++ {
			mean += float64(wav[off+i])
		}
		mean /= frameLength
		for i := 0; i < frameLength; i++ {
			re[i] = float64(wav[off+i]) - mean
		}
		// pre-emphasis (replicate-pad: x'[0] = x[0]-c*x[0])
		for i := frameLength - 1; i >= 1; i-- {
			re[i] -= preemphasis * re[i-1]
		}
		re[0] -= preemphasis * re[0]
		// povey window + zero-pad tail
		for i := 0; i < frameLength; i++ {
			re[i] *= poveyWindow[i]
		}
		for i := frameLength; i < fftSize; i++ {
			re[i] = 0
		}
		for i := range im {
			im[i] = 0
		}
		fftRadix2(re, im)
		// power spectrum -> mel -> log
		row := make([]float32, numMel)
		for b := 0; b < numMel; b++ {
			var e float64
			bank := melBanks[b]
			for k := 0; k <= fftSize/2; k++ {
				if bank[k] != 0 {
					e += bank[k] * (re[k]*re[k] + im[k]*im[k])
				}
			}
			if e < logEpsilon {
				e = logEpsilon
			}
			lv := math.Log(e)
			row[b] = float32(lv)
			colMean[b] += lv
		}
		feats[m] = row
	}
	// CMN: subtract per-bin mean over frames
	for b := 0; b < numMel; b++ {
		colMean[b] /= float64(numFrames)
	}
	for m := 0; m < numFrames; m++ {
		for b := 0; b < numMel; b++ {
			feats[m][b] -= float32(colMean[b])
		}
	}
	return feats
}

// fftRadix2 does an in-place iterative radix-2 FFT (len must be a power of two).
func fftRadix2(re, im []float64) {
	n := len(re)
	for i, j := 1, 0; i < n; i++ {
		bit := n >> 1
		for ; j&bit != 0; bit >>= 1 {
			j ^= bit
		}
		j ^= bit
		if i < j {
			re[i], re[j] = re[j], re[i]
			im[i], im[j] = im[j], im[i]
		}
	}
	for length := 2; length <= n; length <<= 1 {
		ang := -2 * math.Pi / float64(length)
		wlenRe, wlenIm := math.Cos(ang), math.Sin(ang)
		half := length / 2
		for i := 0; i < n; i += length {
			wRe, wIm := 1.0, 0.0
			for k := 0; k < half; k++ {
				a, b := i+k, i+k+half
				vRe := re[b]*wRe - im[b]*wIm
				vIm := re[b]*wIm + im[b]*wRe
				re[b] = re[a] - vRe
				im[b] = im[a] - vIm
				re[a] += vRe
				im[a] += vIm
				wRe, wIm = wRe*wlenRe-wIm*wlenIm, wRe*wlenIm+wIm*wlenRe
			}
		}
	}
}
