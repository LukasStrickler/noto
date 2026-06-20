package bench

import (
	"errors"
	"math"
	"testing"

	corebench "github.com/lukasstrickler/noto/internal/core/bench"
)

func f64ptr(v float64) *float64 { return &v }

// fakeReDecoder returns the same canned replacement for every span — enough to drive
// the attempt loop deterministically without a GPU.
type fakeReDecoder struct {
	words []HypWord
	cost  float64
	fail  bool
}

func (f fakeReDecoder) ReDecode(meetingID string, startSec, endSec float64, method corebench.RepairMethod) ([]HypWord, float64, error) {
	if f.fail {
		return nil, 0, errors.New("re-decode failed")
	}
	return f.words, f.cost, nil
}

// hyp with two low-confidence middle words → one repair span [1,3]; SpeechSec gives
// the budget room to attempt it.
func attemptHyp(midWords [2]string) MeetingHyp {
	return MeetingHyp{
		MeetingID: "M",
		SpeechSec: 30,
		Words: []HypWord{
			{Text: "the", Start: 0, End: 1, Speaker: "A", Confidence: f64ptr(0.95)},
			{Text: midWords[0], Start: 1, End: 2, Speaker: "A", Confidence: f64ptr(0.1)},
			{Text: midWords[1], Start: 2, End: 3, Speaker: "A", Confidence: f64ptr(0.1)},
			{Text: "fox", Start: 3, End: 4, Speaker: "A", Confidence: f64ptr(0.95)},
		},
	}
}

func TestAttemptMeeting_OracleReDecodeIsAcceptedAndImprovesWER(t *testing.T) {
	ref := spliceRef()                            // "the quick brown fox"
	hyp := attemptHyp([2]string{"kwik", "braun"}) // wrong + low-confidence → repair span
	oracle := fakeReDecoder{words: []HypWord{
		{Text: "quick", Start: 1, End: 2, Speaker: "A"},
		{Text: "brown", Start: 2, End: 3, Speaker: "A"},
	}, cost: 0.0001}

	rep, spans, differed, attempted := attemptMeeting(ref, hyp, oracle, 0.5, corebench.MethodAlternateDecode)
	if !attempted || spans != 1 {
		t.Fatalf("expected 1 attempted span, got attempted=%v spans=%d", attempted, spans)
	}
	if differed != 1 {
		t.Errorf("the oracle words (quick/brown) differ from baseline (kwik/braun) → differed=1, got %d", differed)
	}
	if rep.AcceptedRepairs != 1 || rep.NegativeRepairs != 0 {
		t.Errorf("oracle re-decode should be accepted: %+v", rep)
	}
	if rep.NetWERDelta >= 0 {
		t.Errorf("accepted repair must lower benchmark WER, got delta %v", rep.NetWERDelta)
	}
}

func TestAttemptMeeting_BadReDecodeIsNegativeNotAccepted(t *testing.T) {
	ref := spliceRef()
	// Baseline is CORRECT here but low-confidence → a span forms; a bad re-decode
	// must be caught as a NEGATIVE repair, never adopted.
	hyp := attemptHyp([2]string{"quick", "brown"})
	bad := fakeReDecoder{words: []HypWord{
		{Text: "slow", Start: 1, End: 2, Speaker: "A"},
		{Text: "green", Start: 2, End: 3, Speaker: "A"},
	}, cost: 0.0001}

	rep, _, _, attempted := attemptMeeting(ref, hyp, bad, 0.5, corebench.MethodAlternateDecode)
	if !attempted {
		t.Fatal("span should have been attempted")
	}
	if rep.AcceptedRepairs != 0 || rep.NegativeRepairs != 1 {
		t.Errorf("a corrupting re-decode must be negative, not accepted: %+v", rep)
	}
}

func TestAttemptMeeting_FailedReDecodeSkipsCleanly(t *testing.T) {
	ref := spliceRef()
	hyp := attemptHyp([2]string{"kwik", "braun"})
	rep, _, _, attempted := attemptMeeting(ref, hyp, fakeReDecoder{fail: true}, 0.5, corebench.MethodAlternateDecode)
	// The plan still had a candidate (attempted=true), but the failed re-decode
	// produces no outcome — never a crash, never a phantom accept.
	if !attempted {
		t.Fatal("plan had a candidate span")
	}
	if rep.AcceptedRepairs != 0 || rep.NegativeRepairs != 0 {
		t.Errorf("failed re-decode must yield no outcomes: %+v", rep)
	}
}

// The oracle ceiling commits only the edits that lower the WHOLE-transcript WER,
// dropping a locally-promising one that worsens it (the seam cost the 1.1b run
// exposed: 25 locally-accepted netted +0.006, but a 4-edit subset netted -0.0008).
func TestOracleCeiling_KeepsOnlyWholeTranscriptImprovers(t *testing.T) {
	ref := spliceRef() // "the quick brown fox"
	base := []HypWord{
		{Text: "the", Start: 0, End: 1, Speaker: "A"},
		{Text: "kwik", Start: 1, End: 2, Speaker: "A"},  // wrong — fixable
		{Text: "brown", Start: 2, End: 3, Speaker: "A"}, // already correct
		{Text: "fox", Start: 3, End: 4, Speaker: "A"},
	}
	cands := []appliedRepair{
		// A genuine fix: kwik -> quick lowers whole-transcript WER.
		{startSec: 1, endSec: 2, repl: []HypWord{{Text: "quick", Start: 1, End: 2, Speaker: "A"}}, localWERDelta: -1},
		// A seam/regression: replacing the already-correct "brown" raises whole WER.
		{startSec: 2, endSec: 3, repl: []HypWord{{Text: "green", Start: 2, End: 3, Speaker: "A"}}, localWERDelta: -0.5},
	}
	delta, committed := oracleCeiling(ref, base, cands)
	if committed != 1 {
		t.Errorf("only the genuine fix should be committed, got %d", committed)
	}
	if delta >= 0 {
		t.Errorf("the ceiling must improve whole-transcript WER, got %v", delta)
	}
}

func TestMergeRepairReports_SumsFields(t *testing.T) {
	a := corebench.RepairReport{AcceptedRepairs: 2, NegativeRepairs: 1, CostUSD: 0.01, NetWERDelta: -0.1, AcceptedSec: 5}
	b := corebench.RepairReport{AcceptedRepairs: 3, NegativeRepairs: 0, CostUSD: 0.02, NetWERDelta: -0.2, AcceptedSec: 7}
	m := corebench.MergeRepairReports(a, b)
	if m.AcceptedRepairs != 5 || m.NegativeRepairs != 1 || m.AcceptedSec != 12 {
		t.Errorf("merge counts wrong: %+v", m)
	}
	if math.Abs(m.CostUSD-0.03) > 1e-9 || math.Abs(m.NetWERDelta-(-0.3)) > 1e-9 {
		t.Errorf("merge sums wrong: %+v", m)
	}
}
