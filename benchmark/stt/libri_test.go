package stt

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/lukasstrickler/noto/benchmark/metrics"
	sttprov "github.com/lukasstrickler/noto/internal/platform/providers/stt"
)

// TestSTTLibriSpeech is the clean-read-speech WER baseline: each LibriSpeech
// clip is transcribed and scored INDEPENDENTLY (the standard protocol — no
// concatenation, so no artificial clip-junction errors). Env-gated: set
// BENCH_STT_ENGINE (+ the engine's model/runtime env) and BENCH_LIBRI_DIR to the
// corpus built by benchmark/dataset/fetch_librispeech_wer.py. Skips otherwise.
func TestSTTLibriSpeech(t *testing.T) {
	p, ok := buildSTT(t)
	if !ok {
		t.Skip("no STT engine configured (set BENCH_STT_ENGINE)")
	}
	dir := os.Getenv("BENCH_LIBRI_DIR")
	if dir == "" {
		t.Skip("set BENCH_LIBRI_DIR to the librispeech_wer corpus")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "refs.json"))
	if err != nil {
		t.Skipf("no refs.json in %s: %v", dir, err)
	}
	var refs map[string]string
	if err := json.Unmarshal(raw, &refs); err != nil {
		t.Fatalf("refs.json: %v", err)
	}
	if len(refs) == 0 {
		t.Skip("empty corpus")
	}
	names := make([]string, 0, len(refs))
	for n := range refs {
		names = append(names, n)
	}
	sort.Strings(names)

	var sumErr, sumRef int
	var wall time.Duration
	for _, name := range names {
		audio, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		start := time.Now()
		tr, err := p.Transcribe(context.Background(), audio, sttprov.TranscribeOptions{MeetingID: name, Language: "en"})
		wall += time.Since(start)
		if err != nil {
			t.Fatalf("transcribe %s: %v", name, err)
		}
		ref := metrics.Normalize(refs[name])
		w := metrics.WER(ref, hypTokens(tr))
		sumErr += w.Errors()
		sumRef += w.RefLen
	}
	overall := 0.0
	if sumRef > 0 {
		overall = float64(sumErr) / float64(sumRef)
	}
	t.Logf("LibriSpeech clean/validation: %d clips, %d ref words", len(names), sumRef)
	t.Logf("OVERALL WER %.2f%%  (wall %.0fs over %d clips)", overall*100, wall.Seconds(), len(names))

	if os.Getenv("BENCH_DEEP") == "1" {
		const ceiling = 0.12 // clean read speech should be well under this
		if overall > ceiling {
			t.Fatalf("LibriSpeech WER %.2f%% exceeds ceiling %.0f%%", overall*100, ceiling*100)
		}
	}
}
