package speaker

import (
	"context"
	"math"
	"sort"
	"sync"

	"github.com/lukasstrickler/noto/internal/core/artifacts"
	"github.com/lukasstrickler/noto/internal/core/speakers"
)

// Windowing for per-speaker embedding (mirrors the validated bench_precision settings).
const (
	minWindowSec  = 1.5
	maxWindowSec  = 8.0
	maxExemplars  = 12
	embeddingSize = 192
)

// LocalEmbedder is noto's in-process SpeakerEmbedder: ffmpeg decode + ECAPA embedding,
// returning one mean 192-d embedding per provider speaker label. It satisfies
// service.SpeakerEmbedder, so it drops in wherever the HTTP embedder was used — no sidecar.
type LocalEmbedder struct {
	eng    *ECAPA
	ffmpeg string
	mu     sync.Mutex // ECAPA owns one ORT session; serialize Run
}

// NewLocalEmbedder wraps a loaded ECAPA engine and an ffmpeg binary path.
func NewLocalEmbedder(eng *ECAPA, ffmpegPath string) *LocalEmbedder {
	return &LocalEmbedder{eng: eng, ffmpeg: ffmpegPath}
}

// ModelID identifies the embedding space, so profiles/galleries are namespaced per model
// and a model switch can trigger re-embedding.
func (l *LocalEmbedder) ModelID() string { return "ecapa" }

// Close releases the underlying ECAPA ORT session. Implements io.Closer so the
// service model pool can manage the embedder's lifetime (and free RAM on idle).
func (l *LocalEmbedder) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.eng != nil {
		return l.eng.Close()
	}
	return nil
}

// EmbedSpeakers decodes the meeting audio, crops each speaker's diarized turns
// into clean windows, embeds them, and returns one L2-normalized vector per
// provider label. Windows are aggregated with a robust (medoid-anchored)
// centroid so that turns a diarizer misassigned to this speaker are rejected
// instead of poisoning the profile — see speakers.RobustCentroid.
func (l *LocalEmbedder) EmbedSpeakers(ctx context.Context, audio []byte, transcript *artifacts.Transcript) (map[string][]float64, error) {
	windows, err := l.EmbedSpeakerWindows(ctx, audio, transcript)
	if err != nil {
		return nil, err
	}
	out := make(map[string][]float64, len(windows))
	for lbl, exes := range windows {
		if len(exes) == 0 {
			continue
		}
		centroid, cerr := speakers.RobustCentroid(exes, 0)
		if cerr != nil || len(centroid) == 0 {
			continue
		}
		out[lbl] = centroid
	}
	return out, nil
}

// EmbedSpeakerWindows performs the expensive ONNX pass: it returns the per-window
// embeddings for each provider label (longest turns first, capped at
// maxExemplars), without aggregating. Callers that want to compare aggregation
// strategies can embed once here and aggregate cheaply; EmbedSpeakers layers the
// default robust centroid on top.
func (l *LocalEmbedder) EmbedSpeakerWindows(ctx context.Context, audio []byte, transcript *artifacts.Transcript) (map[string][]speakers.Embedding, error) {
	if l == nil || l.eng == nil || len(audio) == 0 || transcript == nil || len(transcript.Speakers) == 0 {
		return nil, nil
	}
	wav, err := DecodeBytes(l.ffmpeg, audio)
	if err != nil {
		return nil, err
	}
	labelByID := make(map[string]string, len(transcript.Speakers))
	for _, sp := range transcript.Speakers {
		labelByID[sp.ID] = sp.ProviderLabel
	}
	type turn struct{ start, end float64 }
	byLabel := make(map[string][]turn)
	for _, seg := range transcript.Segments {
		lbl := labelByID[seg.SpeakerID]
		if lbl == "" || seg.EndSeconds <= seg.StartSeconds {
			continue
		}
		byLabel[lbl] = append(byLabel[lbl], turn{seg.StartSeconds, seg.EndSeconds})
	}

	out := make(map[string][]speakers.Embedding, len(byLabel))
	l.mu.Lock()
	defer l.mu.Unlock()
	for lbl, turns := range byLabel {
		// longest turns first -> best exemplars
		sort.Slice(turns, func(i, j int) bool {
			return turns[i].end-turns[i].start > turns[j].end-turns[j].start
		})
		var exes []speakers.Embedding
	turns:
		for _, t := range turns {
			for w0 := t.start; w0+minWindowSec <= t.end; w0 += maxWindowSec {
				w1 := w0 + maxWindowSec
				if w1 > t.end {
					w1 = t.end
				}
				a, b, ok := windowBounds(w0, w1, len(wav))
				if !ok {
					continue
				}
				emb, err := l.eng.Embed(wav[a:b])
				if err != nil {
					continue
				}
				v := make(speakers.Embedding, len(emb))
				for i, x := range emb {
					v[i] = float64(x)
				}
				exes = append(exes, normalize64(v))
				if len(exes) >= maxExemplars {
					break turns
				}
			}
		}
		if len(exes) > 0 {
			out[lbl] = exes
		}
	}
	return out, nil
}

// windowBounds maps a [w0,w1) second-window to sample indices into a wav of wavLen
// samples, clamping BOTH ends into [0,wavLen]. It returns ok=false when the clamped
// window is empty or shorter than minWindowSec. Clamping the lower bound is the
// point: ValidateTranscript only rejects end<start (transcript.go), and the
// timestamp normalizer is unwired, so a provider/normalization quirk can leak a
// segment with a NEGATIVE start through to here — which would give a<0 and panic
// wav[a:b], failing the whole embed step and losing identity for the meeting. The
// negative span simply doesn't exist in the audio, so we embed the valid portion.
func windowBounds(w0, w1 float64, wavLen int) (a, b int, ok bool) {
	a, b = int(w0*sampleRate), int(w1*sampleRate)
	if a < 0 {
		a = 0
	}
	if b > wavLen {
		b = wavLen
	}
	if b-a < int(minWindowSec*sampleRate) {
		return 0, 0, false
	}
	return a, b, true
}

func normalize64(v []float64) []float64 {
	var n float64
	for _, x := range v {
		n += x * x
	}
	if n == 0 {
		return v
	}
	n = 1.0 / (math.Sqrt(n) + 1e-9)
	for i := range v {
		v[i] *= n
	}
	return v
}
