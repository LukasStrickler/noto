package bench

import (
	"math"
	"testing"

	corebench "github.com/lukasstrickler/noto/internal/core/bench"
)

// A runReDecoder serves the span's words from an alternate run by midpoint, the same
// membership rule the splice uses — so the words it returns are exactly the ones the
// measure step substitutes.
func TestRunReDecoder_SlicesSpanByMidpointAndPricesSTTOnly(t *testing.T) {
	dec := &runReDecoder{
		alt: map[string][]HypWord{
			"M": {
				{Text: "the", Start: 0, End: 1, Speaker: "A"},   // mid 0.5 — out
				{Text: "quick", Start: 1, End: 2, Speaker: "A"}, // mid 1.5 — in
				{Text: "brown", Start: 2, End: 3, Speaker: "A"}, // mid 2.5 — in
				{Text: "fox", Start: 3, End: 4, Speaker: "A"},   // mid 3.5 — out
			},
		},
		sttCostPerAudioSec: 0.001,
	}

	words, cost, err := dec.ReDecode("M", 1, 3, corebench.MethodAlternateDecode)
	if err != nil {
		t.Fatalf("ReDecode: %v", err)
	}
	if len(words) != 2 || words[0].Text != "quick" || words[1].Text != "brown" {
		t.Errorf("expected the two midpoint-in-span words, got %+v", words)
	}
	if math.Abs(cost-0.002) > 1e-9 { // 2 audio-sec × $0.001/sec, STT-only
		t.Errorf("span cost = %v, want 0.002 (2s × rate)", cost)
	}
}

func TestRunReDecoder_UnknownMeetingErrors(t *testing.T) {
	dec := &runReDecoder{alt: map[string][]HypWord{"M": nil}}
	if _, _, err := dec.ReDecode("OTHER", 0, 1, corebench.MethodAlternateDecode); err == nil {
		t.Fatal("re-decoding a meeting the alternate run never saw must error, not return empty")
	}
}

// The whole loop runs against a run-pair decoder: a corrected alternate decode is
// accepted and drops WER, proving the cheap two-run path produces a real outcome.
func TestAttemptMeeting_WithRunReDecoder_AcceptsCorrection(t *testing.T) {
	ref := spliceRef()                            // "the quick brown fox"
	hyp := attemptHyp([2]string{"kwik", "braun"}) // wrong + low-confidence span [1,3]
	dec := &runReDecoder{
		alt: map[string][]HypWord{
			hyp.Key(): {
				{Text: "the", Start: 0, End: 1, Speaker: "A"},
				{Text: "quick", Start: 1, End: 2, Speaker: "A"}, // the fix
				{Text: "brown", Start: 2, End: 3, Speaker: "A"}, // the fix
				{Text: "fox", Start: 3, End: 4, Speaker: "A"},
			},
		},
		sttCostPerAudioSec: 0.0001,
	}

	rep, spans, attempted := attemptMeeting(ref, hyp, dec, 0.5, corebench.MethodAlternateDecode)
	if !attempted || spans != 1 {
		t.Fatalf("expected 1 attempted span, got attempted=%v spans=%d", attempted, spans)
	}
	if rep.AcceptedRepairs != 1 || rep.NegativeRepairs != 0 {
		t.Errorf("the corrected alternate decode should be accepted: %+v", rep)
	}
	if rep.NetWERDelta >= 0 {
		t.Errorf("accepted repair must lower benchmark WER, got delta %v", rep.NetWERDelta)
	}
	if rep.CostUSD <= 0 {
		t.Errorf("an attempted span must charge STT-only cost, got %v", rep.CostUSD)
	}
}
