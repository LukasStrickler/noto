package bench

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/lukasstrickler/noto/benchmark/dataset"
	corebench "github.com/lukasstrickler/noto/internal/core/bench"
)

// diarCostPerAudioSec derives the diar-only $/audio-sec as (total − asr) / audio, robust
// to the per-stage attribution gap (the diar wall is sometimes left unsplit).
func TestDiarCostPerAudioSec_DerivesDiarRateFromTrace(t *testing.T) {
	dir := t.TempDir()
	writeJSONFixture(t, filepath.Join(dir, "trace_summary.json"), corebench.TraceSummary{
		PerMeeting: []corebench.PerMeetingTrace{
			{MeetingID: "M1", AudioSec: 100, USD: 0.10, USDByStage: map[string]float64{"asr": 0.02}},
			{MeetingID: "M2", AudioSec: 100, USD: 0.20, USDByStage: map[string]float64{"asr": 0.04}},
		},
	})
	// diar = (0.10-0.02) + (0.20-0.04) = 0.24 over 200 audio-sec → 0.0012/sec.
	rate, err := diarCostPerAudioSec(dir)
	if err != nil {
		t.Fatal(err)
	}
	if rate < 0.00119 || rate > 0.00121 {
		t.Errorf("diar rate = %v, want ~0.0012", rate)
	}
}

func TestDiarCostPerAudioSec_ZeroWhenTraceMissing(t *testing.T) {
	if rate, _ := diarCostPerAudioSec(t.TempDir()); rate != 0 {
		t.Errorf("missing trace → rate 0, got %v", rate)
	}
}

// fakeReDiarizer returns canned turns for every overlap span — enough to drive the
// diar attempt loop deterministically without a GPU.
type fakeReDiarizer struct {
	turns []HypTurn
	cost  float64
	fail  bool
}

func (f fakeReDiarizer) ReDiarize(meetingID string, startSec, endSec float64) ([]HypTurn, float64, error) {
	if f.fail {
		return nil, 0, errors.New("re-diarize failed")
	}
	return f.turns, f.cost, nil
}

// ref: A [0,6] and B [4,10] — they overlap in [4,6].
func diarOverlapRef() []dataset.Turn {
	return []dataset.Turn{
		{Speaker: "A", StartSeconds: 0, EndSeconds: 6},
		{Speaker: "B", StartSeconds: 4, EndSeconds: 10},
	}
}

func diarOverlapHyp() MeetingHyp {
	// Baseline misses B in the overlap: it labels the whole thing A, so [4,10] is a
	// confusion (B's turn is attributed to A).
	return MeetingHyp{
		MeetingID: "M",
		Turns:     []HypTurn{{Speaker: "A", Start: 0, End: 10}},
	}
}

func TestAttemptDiarMeeting_OracleReDiarizationAcceptedAndLowersDER(t *testing.T) {
	ref := diarOverlapRef()
	hyp := diarOverlapHyp()
	// A correct re-diarization of the overlap region recovers B there.
	oracle := fakeReDiarizer{turns: []HypTurn{
		{Speaker: "A", Start: 4, End: 6},
		{Speaker: "B", Start: 4, End: 6},
	}, cost: 0.0001}

	rep, regions, differed, attempted := attemptDiarMeeting(ref, hyp, oracle)
	if !attempted || regions != 1 {
		t.Fatalf("expected 1 overlap region attempted, got attempted=%v regions=%d", attempted, regions)
	}
	if differed != 1 {
		t.Errorf("the re-diarization adds speaker B in the span → differed=1, got %d", differed)
	}
	if rep.AcceptedRepairs != 1 || rep.NegativeRepairs != 0 {
		t.Errorf("a DER-lowering re-diarization must be accepted: %+v", rep)
	}
	if rep.NetWERDelta >= 0 { // primary delta carries DER here
		t.Errorf("accepted re-diarization must lower DER, got delta %v", rep.NetWERDelta)
	}
	if rep.CeilingWERDelta >= 0 || rep.CeilingAccepted < 1 {
		t.Errorf("the oracle ceiling must keep the real fix: delta=%v accepted=%d", rep.CeilingWERDelta, rep.CeilingAccepted)
	}
}

func TestAttemptDiarMeeting_BadReDiarizationIsNegative(t *testing.T) {
	ref := diarOverlapRef()
	// Baseline is already correct in the overlap region.
	hyp := MeetingHyp{MeetingID: "M", Turns: []HypTurn{
		{Speaker: "A", Start: 0, End: 6},
		{Speaker: "B", Start: 4, End: 10},
	}}
	// A bad re-diarization collapses the overlap to a single wrong speaker.
	bad := fakeReDiarizer{turns: []HypTurn{{Speaker: "A", Start: 4, End: 6}}, cost: 0.0001}

	rep, _, _, attempted := attemptDiarMeeting(ref, hyp, bad)
	if !attempted {
		t.Fatal("the overlap region should have been attempted")
	}
	if rep.AcceptedRepairs != 0 || rep.NegativeRepairs == 0 {
		t.Errorf("a DER-raising re-diarization must be negative, not accepted: %+v", rep)
	}
}

func TestAttemptDiarMeeting_FailedReDiarizationSkipsCleanly(t *testing.T) {
	ref := diarOverlapRef()
	hyp := diarOverlapHyp()
	rep, regions, _, attempted := attemptDiarMeeting(ref, hyp, fakeReDiarizer{fail: true})
	if !attempted || regions != 1 {
		t.Fatalf("the plan had an overlap region, got attempted=%v regions=%d", attempted, regions)
	}
	if rep.AcceptedRepairs != 0 || rep.NegativeRepairs != 0 {
		t.Errorf("a failed re-diarization must yield no outcomes: %+v", rep)
	}
}

func TestRunReDiarizer_SlicesSpanAndErrorsOnUnknown(t *testing.T) {
	dec := &runReDiarizer{
		alt: map[string][]HypTurn{
			"M": {
				{Speaker: "A", Start: 0, End: 5},  // clipped to [4,5)
				{Speaker: "B", Start: 5, End: 12}, // clipped to [5,6)
			},
		},
		costPerSec: 0.0001,
	}
	turns, cost, err := dec.ReDiarize("M", 4, 6)
	if err != nil {
		t.Fatalf("ReDiarize: %v", err)
	}
	want := []HypTurn{{Speaker: "A", Start: 4, End: 5}, {Speaker: "B", Start: 5, End: 6}}
	if len(turns) != 2 || turns[0] != want[0] || turns[1] != want[1] {
		t.Errorf("expected span-clipped turns %+v, got %+v", want, turns)
	}
	if cost <= 0 {
		t.Errorf("a re-diarized span must charge cost, got %v", cost)
	}
	if _, _, err := dec.ReDiarize("OTHER", 0, 1); err == nil {
		t.Error("re-diarizing an unknown meeting must error")
	}
}
