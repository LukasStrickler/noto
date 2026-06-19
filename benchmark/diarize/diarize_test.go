// Package diarize is the ATOMIC diarization benchmark: a Diarizer is fed
// ground-truth audio and scored in isolation (DER + attribution + RTF) against
// the reference turns. "Atomic" = golden input, so the number is the diarizer's
// own error, not anything compounded from upstream.
//
// Two layers:
//   - fast harness tests (fake diarizers, synthetic turns) prove the scorer
//     wiring end-to-end with no assets — they run in `make test`.
//   - TestDiarizeAMI is the real, env-gated bench: it builds a Diarizer from the
//     environment (the bundled AssemblyAI diarizer is the reference column until a
//     local diarizer lands) and scores it on the AMI meetings. It t.Skips when
//     neither a provider nor the AMI assets are present.
package diarize

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
	diar "github.com/lukasstrickler/noto/internal/platform/providers/diarize"
)

// turnsToSegments adapts diarizer output to the scorer's input shape.
func turnsToSegments(turns []diar.Turn) []metrics.Segment {
	segs := make([]metrics.Segment, len(turns))
	for i, t := range turns {
		segs[i] = metrics.Segment{Speaker: t.Speaker, Start: t.StartSeconds, End: t.EndSeconds}
	}
	return segs
}

// diarScore is one meeting's diarization result.
type diarScore struct {
	DER         metrics.DERResult
	Attribution float64
	RTF         float64
}

// score runs the scorers for one meeting. wall is the diarizer's wall time;
// audioSec the meeting's reference duration.
func score(ref []metrics.Segment, hyp []diar.Turn, wall time.Duration, audioSec float64) diarScore {
	hs := turnsToSegments(hyp)
	return diarScore{
		DER:         metrics.DER(ref, hs, metrics.DefaultDEROptions()),
		Attribution: metrics.AttributionAccuracy(ref, hs),
		RTF:         metrics.Timing{Stage: "diarize", Wall: wall, AudioSeconds: audioSec}.RTF(),
	}
}

// fakeDiarizer returns canned turns regardless of the audio — the harness probe.
type fakeDiarizer struct{ turns []diar.Turn }

func (f fakeDiarizer) ProviderID() string { return "fake" }
func (f fakeDiarizer) Diarize(context.Context, []byte, diar.DiarizeOptions) ([]diar.Turn, error) {
	return f.turns, nil
}

var _ diar.Diarizer = fakeDiarizer{}

// TestDERHarness proves the provider→scorer wiring: a perfect diarizer scores 0
// DER / 1.0 attribution; one speaker swallowing the other is half confusion.
func TestDERHarness(t *testing.T) {
	ref := []metrics.Segment{
		{Speaker: "A", Start: 0, End: 5},
		{Speaker: "B", Start: 5, End: 10},
	}
	d, err := fakeDiarizer{turns: []diar.Turn{
		{Speaker: "X", StartSeconds: 0, EndSeconds: 5},
		{Speaker: "Y", StartSeconds: 5, EndSeconds: 10},
	}}.Diarize(context.Background(), nil, diar.DiarizeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	perfect := score(ref, d, time.Second, 10)
	if perfect.DER.Rate != 0 {
		t.Errorf("perfect DER = %v, want 0", perfect.DER.Rate)
	}
	if perfect.Attribution != 1 {
		t.Errorf("perfect attribution = %v, want 1", perfect.Attribution)
	}
	if perfect.RTF != 0.1 {
		t.Errorf("RTF = %v, want 0.1", perfect.RTF)
	}

	merged := score(ref, []diar.Turn{{Speaker: "X", StartSeconds: 0, EndSeconds: 10}}, time.Second, 10)
	if merged.DER.Rate != 0.5 {
		t.Errorf("merged-speaker DER = %v, want 0.5", merged.DER.Rate)
	}
	if merged.Attribution != 0.5 {
		t.Errorf("merged-speaker attribution = %v, want 0.5", merged.Attribution)
	}
}

// buildDiarizer constructs the diarizer under test from the environment: the
// local engine named by BENCH_DIAR_ENGINE, built through the runtime-agnostic
// registry (models from BENCH_DIAR_MODELS). No engine named → skip. This is the
// seam a real local diarizer registers into. (A BundledDiarizer over a cloud STT
// remains available in code as a reference column, but noto is pivoting off it.)
func buildDiarizer(t *testing.T) (diar.Diarizer, bool) {
	t.Helper()
	name := os.Getenv("BENCH_DIAR_ENGINE")
	if name == "" {
		return nil, false
	}
	d, err := diar.NewLocalDiarizerByName(name, diar.EngineConfig{ModelDir: os.Getenv("BENCH_DIAR_MODELS")})
	if err != nil {
		t.Skipf("diarizer engine %q: %v", name, err)
		return nil, false
	}
	return d, true
}

// amiDir locates the AMI assets fetched by ../identity/fetch.py.
func amiDir() string { return filepath.Join("..", "identity", "ami") }

// loadAMIMeetings discovers AMI meetings present on disk (an .rttm with a
// matching .wav).
func loadAMIMeetings(t *testing.T) []dataset.Meeting {
	t.Helper()
	rttms, _ := filepath.Glob(filepath.Join(amiDir(), "*.rttm"))
	var meetings []dataset.Meeting
	for _, r := range rttms {
		id := trimExt(filepath.Base(r))
		wav := filepath.Join(amiDir(), id+".wav")
		if !fileExists(wav) {
			continue
		}
		turns, err := dataset.LoadRTTM(r)
		if err != nil {
			t.Fatalf("LoadRTTM %s: %v", r, err)
		}
		meetings = append(meetings, dataset.Meeting{ID: id, AudioPath: wav, Turns: turns})
	}
	sort.Slice(meetings, func(i, j int) bool { return meetings[i].ID < meetings[j].ID })
	return bench.Select(t, meetings)
}

func TestDiarizeAMI(t *testing.T) {
	d, ok := buildDiarizer(t)
	if !ok {
		t.Skip("no diarizer engine configured (set BENCH_DIAR_ENGINE to a registered local engine)")
	}
	meetings := loadAMIMeetings(t)
	if len(meetings) == 0 {
		t.Skip("no AMI assets (run: python3 benchmark/identity/fetch.py)")
	}

	var sumErr, sumRef, sumAttr float64
	t.Logf("%-10s %8s %8s %8s %8s %7s", "meeting", "DER", "miss", "FA", "conf", "RTF")
	for _, m := range meetings {
		audio, err := os.ReadFile(m.AudioPath)
		if err != nil {
			t.Fatalf("read %s: %v", m.AudioPath, err)
		}
		start := time.Now()
		turns, err := d.Diarize(context.Background(), audio, diar.DiarizeOptions{MeetingID: m.ID})
		wall := time.Since(start)
		if err != nil {
			t.Fatalf("Diarize %s: %v", m.ID, err)
		}
		ref := m.Segments()
		s := score(ref, turns, wall, meetingDuration(ref))
		t.Logf("%-10s %7.1f%% %7.1f%% %7.1f%% %7.1f%% %7.2f",
			m.ID, s.DER.Rate*100, pct(s.DER.Missed, s.DER.RefSpeech), pct(s.DER.FalseAlarm, s.DER.RefSpeech),
			pct(s.DER.Confusion, s.DER.RefSpeech), s.RTF)
		sumErr += s.DER.Total()
		sumRef += s.DER.RefSpeech
		sumAttr += s.Attribution
	}
	overall := 0.0
	if sumRef > 0 {
		overall = sumErr / sumRef
	}
	t.Logf("OVERALL DER %.1f%% over %.0fs ref speech; mean attribution %.1f%%",
		overall*100, sumRef, sumAttr/float64(len(meetings))*100)

	if os.Getenv("BENCH_DEEP") == "1" {
		// A loose ceiling so a gross regression fails the gate; the real baseline
		// is pinned per-provider in benchmark/e2e (Task #8).
		const ceiling = 0.50
		if overall > ceiling {
			t.Fatalf("overall DER %.1f%% exceeds ceiling %.0f%%", overall*100, ceiling*100)
		}
	}
}

func meetingDuration(ref []metrics.Segment) float64 {
	var end float64
	for _, s := range ref {
		if s.End > end {
			end = s.End
		}
	}
	return end
}

func pct(part, whole float64) float64 {
	if whole <= 0 {
		return 0
	}
	return part / whole * 100
}

func trimExt(name string) string { return name[:len(name)-len(filepath.Ext(name))] }

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}
