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
	sttprov "github.com/lukasstrickler/noto/internal/platform/providers/stt"
)

func TestSTTSynthetic(t *testing.T) {
	dir := os.Getenv("BENCH_SYNTH_DIR")
	if dir == "" {
		t.Skip("set BENCH_SYNTH_DIR to the synthetic_meetings corpus")
	}
	p, ok := buildSTT(t)
	if !ok {
		t.Skip("no STT engine configured (set BENCH_STT_ENGINE to a registered engine)")
	}
	meetings := syntheticMeetings(t, dir)
	if len(meetings) == 0 {
		t.Skip("no synthetic meetings")
	}

	// BENCH_STT_BATCH=1: feed the WHOLE corpus through one warm engine pass
	// (one model load + CUDA init for the run, the GPU fed back-to-back) — the
	// cheap benchmark-throughput shape. Scores are identical to the per-meeting
	// path; only per-meeting RTF attribution is lost, so the honest single-stream
	// (prod) runs keep the default path.
	if os.Getenv("BENCH_STT_BATCH") == "1" {
		if lp, ok := p.(*sttprov.LocalSTT); ok && lp.BatchCapable() {
			runSTTSyntheticBatched(t, lp, meetings)
			return
		}
		t.Logf("BENCH_STT_BATCH=1 but engine is not batch-capable; per-meeting path")
	}

	type sttRes struct {
		errs, ref int
		wall      time.Duration
		audio     float64
	}
	t.Logf("%-8s %8s %8s %7s", "meeting", "WER", "words", "RTF")
	results := bench.RunMeetings(t, meetings, func(t *testing.T, m dataset.Meeting) sttRes {
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
		w := metrics.WER(m.Reference(), hypTokens(tr))
		audioSec := refDuration(m.Words)
		rtf := metrics.Timing{Stage: "stt", Wall: wall, AudioSeconds: audioSec}.RTF()
		t.Logf("%-8s %7.1f%% %8d %7.2f", m.ID, w.Rate*100, w.RefLen, rtf)
		return sttRes{errs: w.Errors(), ref: w.RefLen, wall: wall, audio: audioSec}
	})

	var sumErr, sumRef int
	var sumWall time.Duration
	var sumAudio float64
	for _, r := range results {
		sumErr += r.errs
		sumRef += r.ref
		sumWall += r.wall
		sumAudio += r.audio
	}
	overall := 0.0
	if sumRef > 0 {
		overall = float64(sumErr) / float64(sumRef)
	}
	t.Logf("OVERALL SYNTH WER %.1f%% over %d ref words; RTF %.2f",
		overall*100, sumRef, metrics.Timing{Stage: "stt", Wall: sumWall, AudioSeconds: sumAudio}.RTF())
}

// runSTTSyntheticBatched scores the same corpus through LocalSTT.TranscribeBatch:
// one timed call, per-meeting WER, and the overall line in the exact format the
// Modal KPI parser reads. The RTF is the batch wall over the summed audio — the
// throughput number this shape exists to improve.
func runSTTSyntheticBatched(t *testing.T, p *sttprov.LocalSTT, meetings []dataset.Meeting) {
	audios := make([][]byte, len(meetings))
	opts := make([]sttprov.TranscribeOptions, len(meetings))
	for i, m := range meetings {
		audio, err := os.ReadFile(m.AudioPath)
		if err != nil {
			t.Fatalf("read %s: %v", m.AudioPath, err)
		}
		audios[i] = audio
		opts[i] = sttprov.TranscribeOptions{MeetingID: m.ID, Language: "en"}
	}
	start := time.Now()
	trs, err := p.TranscribeBatch(context.Background(), audios, opts)
	wall := time.Since(start)
	if err != nil {
		t.Fatalf("TranscribeBatch: %v", err)
	}

	var sumErr, sumRef int
	var sumAudio float64
	t.Logf("batched: %d meetings in one warm pass (per-meeting RTF not attributable)", len(meetings))
	t.Logf("%-8s %8s %8s", "meeting", "WER", "words")
	for i, m := range meetings {
		w := metrics.WER(m.Reference(), hypTokens(trs[i]))
		t.Logf("%-8s %7.1f%% %8d", m.ID, w.Rate*100, w.RefLen)
		sumErr += w.Errors()
		sumRef += w.RefLen
		sumAudio += refDuration(m.Words)
	}
	overall := 0.0
	if sumRef > 0 {
		overall = float64(sumErr) / float64(sumRef)
	}
	t.Logf("OVERALL SYNTH WER %.1f%% over %d ref words; RTF %.2f",
		overall*100, sumRef, metrics.Timing{Stage: "stt", Wall: wall, AudioSeconds: sumAudio}.RTF())
}

func syntheticMeetings(t *testing.T, dir string) []dataset.Meeting {
	t.Helper()
	refs, _ := filepath.Glob(filepath.Join(dir, "*.words.json"))
	var meetings []dataset.Meeting
	for _, r := range refs {
		id := base(r, ".words.json")
		wav := filepath.Join(dir, id+".wav")
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
