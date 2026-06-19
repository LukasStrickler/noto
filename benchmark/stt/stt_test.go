// Package stt is the ATOMIC speech-to-text benchmark: an STTProvider is fed
// ground-truth audio and scored in isolation (WER + RTF) against the reference
// transcript. "Atomic" = golden input, so the number is the recognizer's own
// error.
//
// Two layers (mirroring the diarize bench):
//   - fast harness tests (a fake STT, synthetic words) prove the WER/RTF wiring
//     with no assets — they run in `make test`.
//   - TestSTTAMI is the real, env-gated bench: it builds an STTProvider from the
//     environment, transcribes the AMI meetings, and scores WER against the
//     reference words. It t.Skips when no provider or no reference words are
//     present (the AMI word references are produced into benchmark/dataset/words/).
package stt

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/lukasstrickler/noto/benchmark/dataset"
	"github.com/lukasstrickler/noto/benchmark/internal/bench"
	"github.com/lukasstrickler/noto/benchmark/metrics"
	"github.com/lukasstrickler/noto/internal/core/artifacts"
	sttprov "github.com/lukasstrickler/noto/internal/platform/providers/stt"
)

// hypTokens flattens a transcript into a normalized token stream the way the WER
// scorer wants: prefer word-level output (time-ordered), fall back to segment
// text when a provider returns no words.
func hypTokens(tr *artifacts.Transcript) []string {
	var text string
	if len(tr.Words) > 0 {
		ws := append([]artifacts.Word(nil), tr.Words...)
		sort.SliceStable(ws, func(i, j int) bool { return ws[i].StartSeconds < ws[j].StartSeconds })
		for _, w := range ws {
			text += " " + w.Text
		}
	} else {
		segs := append([]artifacts.Segment(nil), tr.Segments...)
		sort.SliceStable(segs, func(i, j int) bool { return segs[i].StartSeconds < segs[j].StartSeconds })
		for _, s := range segs {
			text += " " + s.Text
		}
	}
	return metrics.Normalize(text)
}

// fakeSTT returns a canned transcript regardless of the audio — the harness probe.
type fakeSTT struct{ tr *artifacts.Transcript }

func (f fakeSTT) ProviderID() string { return "fake" }
func (f fakeSTT) FeatureMap() sttprov.ProviderFeatures {
	return sttprov.ProviderFeatures{ProviderID: "fake"}
}
func (f fakeSTT) Transcribe(context.Context, []byte, sttprov.TranscribeOptions) (*artifacts.Transcript, error) {
	return f.tr, nil
}

var _ sttprov.STTProvider = fakeSTT{}

func wordsTranscript(words ...string) *artifacts.Transcript {
	tr := &artifacts.Transcript{}
	for i, w := range words {
		tr.Words = append(tr.Words, artifacts.Word{Text: w, StartSeconds: float64(i)})
	}
	return tr
}

// TestWERHarness proves the provider→scorer wiring: a perfect recognizer scores
// 0 WER; a one-word substitution over four words is 25%.
func TestWERHarness(t *testing.T) {
	ref := metrics.Normalize("the quick brown fox")

	perfect := fakeSTT{tr: wordsTranscript("The", "quick", "brown", "fox")}
	got, _ := perfect.Transcribe(context.Background(), nil, sttprov.TranscribeOptions{})
	if w := metrics.WER(ref, hypTokens(got)); w.Rate != 0 {
		t.Errorf("perfect WER = %v, want 0", w.Rate)
	}

	wrong := fakeSTT{tr: wordsTranscript("the", "quick", "brown", "dog")}
	got2, _ := wrong.Transcribe(context.Background(), nil, sttprov.TranscribeOptions{})
	if w := metrics.WER(ref, hypTokens(got2)); w.Rate != 0.25 {
		t.Errorf("one-sub WER = %v, want 0.25", w.Rate)
	}

	// RTF wiring: 30 s wall over 60 s audio = 0.5.
	if rtf := (metrics.Timing{Stage: "stt", Wall: 30 * time.Second, AudioSeconds: 60}).RTF(); rtf != 0.5 {
		t.Errorf("RTF = %v, want 0.5", rtf)
	}
}

// buildSTT constructs the recognizer under test from the environment: the local
// engine named by BENCH_STT_ENGINE, built through the runtime-agnostic registry
// (models from BENCH_STT_MODELS). No engine named → skip. This is the seam a real
// local STT runtime registers into; until one is wired, the bench skips.
func buildSTT(t *testing.T) (sttprov.STTProvider, bool) {
	t.Helper()
	name := os.Getenv("BENCH_STT_ENGINE")
	if name == "" {
		return nil, false
	}
	p, err := sttprov.NewLocalSTTByName(name, sttprov.EngineConfig{ModelDir: os.Getenv("BENCH_STT_MODELS")})
	if err != nil {
		t.Skipf("STT engine %q: %v", name, err)
		return nil, false
	}
	return p, true
}

func amiDir() string   { return filepath.Join("..", "identity", "ami") }
func wordsDir() string { return filepath.Join("..", "dataset", "words") }

// loadMeetingsWithWords discovers AMI meetings that have BOTH audio and reference
// words on disk.
func loadMeetingsWithWords(t *testing.T) []dataset.Meeting {
	t.Helper()
	refs, _ := filepath.Glob(filepath.Join(wordsDir(), "*.words.json"))
	var meetings []dataset.Meeting
	for _, r := range refs {
		id := base(r, ".words.json")
		wav := filepath.Join(amiDir(), id+".wav")
		if !fileExists(wav) {
			continue
		}
		words, err := dataset.LoadWords(r)
		if err != nil {
			t.Fatalf("LoadWords %s: %v", r, err)
		}
		meetings = append(meetings, dataset.Meeting{ID: id, AudioPath: wav, Words: words})
	}
	sort.Slice(meetings, func(i, j int) bool { return meetings[i].ID < meetings[j].ID })
	return bench.Select(t, meetings)
}

func TestSTTAMI(t *testing.T) {
	p, ok := buildSTT(t)
	if !ok {
		t.Skip("no STT engine configured (set BENCH_STT_ENGINE to a registered local engine)")
	}
	meetings := loadMeetingsWithWords(t)
	if len(meetings) == 0 {
		t.Skip("no AMI reference words (produce benchmark/dataset/words/<id>.words.json)")
	}

	var sumErr, sumRef int
	t.Logf("%-10s %8s %8s %7s", "meeting", "WER", "words", "RTF")
	for _, m := range meetings {
		audio, err := os.ReadFile(m.AudioPath)
		if err != nil {
			t.Fatalf("read %s: %v", m.AudioPath, err)
		}
		start := time.Now()
		tr, err := p.Transcribe(context.Background(), audio, sttprov.TranscribeOptions{MeetingID: m.ID, Language: "en"})
		wall := time.Since(start)
		if err != nil {
			t.Fatalf("Transcribe %s: %v", m.ID, err)
		}
		ref := m.Reference()
		w := metrics.WER(ref, hypTokens(tr))
		rtf := metrics.Timing{Stage: "stt", Wall: wall, AudioSeconds: refDuration(m.Words)}.RTF()
		t.Logf("%-10s %7.1f%% %8d %7.2f", m.ID, w.Rate*100, w.RefLen, rtf)
		sumErr += w.Errors()
		sumRef += w.RefLen
	}
	overall := 0.0
	if sumRef > 0 {
		overall = float64(sumErr) / float64(sumRef)
	}
	t.Logf("OVERALL WER %.1f%% over %d ref words", overall*100, sumRef)

	if os.Getenv("BENCH_DEEP") == "1" {
		const ceiling = 0.40 // loose gate; real baseline pinned in benchmark/e2e
		if overall > ceiling {
			t.Fatalf("overall WER %.1f%% exceeds ceiling %.0f%%", overall*100, ceiling*100)
		}
	}
}

func refDuration(words []dataset.Word) float64 {
	var end float64
	for _, w := range words {
		if w.EndSeconds > end {
			end = w.EndSeconds
		}
	}
	return end
}

func base(path, suffix string) string {
	b := filepath.Base(path)
	return b[:len(b)-len(suffix)]
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}
