package bench

import (
	"math"
	"testing"

	"github.com/lukasstrickler/noto/benchmark/dataset"
	corebench "github.com/lukasstrickler/noto/internal/core/bench"
)

// ref: A speaks [0,4], B speaks [4,8].
func diarRef() []dataset.Turn {
	return []dataset.Turn{
		{Speaker: "A", StartSeconds: 0, EndSeconds: 4},
		{Speaker: "B", StartSeconds: 4, EndSeconds: 8},
	}
}

func TestMeasureDiarSplice_FixingSpeakerConfusionImprovesDER(t *testing.T) {
	ref := diarRef()
	// Baseline calls the whole meeting speaker A — B's [4,8] is a confusion.
	base := []HypTurn{{Speaker: "A", Start: 0, End: 8}}
	// A re-diarization of [4,8) recovers B.
	repl := []HypTurn{{Speaker: "B", Start: 4, End: 8}}

	m := MeasureDiarSplice(ref, base, 4, 8, repl)
	if m.DERBefore <= 0 {
		t.Fatalf("baseline confusion should have DER > 0, got %v", m.DERBefore)
	}
	if m.DERAfter != 0 {
		t.Errorf("recovering B should drive DER to 0, got %v", m.DERAfter)
	}
	if m.DERDelta >= 0 {
		t.Errorf("a real re-diarization fix must lower DER, got %v", m.DERDelta)
	}
	out := m.Outcome(corebench.RepairSpan{StartSec: 4, EndSec: 8}, corebench.MethodAlternateDecode, 0.001)
	if !out.Accepted || out.Negative {
		t.Errorf("a DER-improving re-diarization must be accepted: %+v", out)
	}
}

func TestMeasureDiarSplice_BreakingCorrectDiarizationRegresses(t *testing.T) {
	ref := diarRef()
	// Baseline diarization is perfect.
	base := []HypTurn{{Speaker: "A", Start: 0, End: 4}, {Speaker: "B", Start: 4, End: 8}}
	// A bad re-diarization mislabels [4,8) as A.
	repl := []HypTurn{{Speaker: "A", Start: 4, End: 8}}

	m := MeasureDiarSplice(ref, base, 4, 8, repl)
	if m.DERBefore != 0 {
		t.Fatalf("perfect baseline should have DER 0, got %v", m.DERBefore)
	}
	if m.DERDelta <= 0 {
		t.Errorf("a corrupting re-diarization must raise DER, got %v", m.DERDelta)
	}
	out := m.Outcome(corebench.RepairSpan{StartSec: 4, EndSec: 8}, corebench.MethodAlternateDecode, 0.001)
	if out.Accepted || !out.Negative {
		t.Errorf("a regressing re-diarization must be negative, not accepted: %+v", out)
	}
}

// Whole-meeting DER stays sensitive to a SMALL (multi-second) fix because DER error
// is denominated in seconds, not words — so a per-span accept decision needs no local
// measure (which DER's permutation invariance would break anyway). A 2 s fix in a
// 100 s meeting must clear the accept threshold.
func TestMeasureDiarSplice_SmallFixClearsAcceptThreshold(t *testing.T) {
	ref := []dataset.Turn{
		{Speaker: "A", StartSeconds: 0, EndSeconds: 50},
		{Speaker: "B", StartSeconds: 50, EndSeconds: 52},
		{Speaker: "A", StartSeconds: 52, EndSeconds: 100},
	}
	base := []HypTurn{{Speaker: "A", Start: 0, End: 100}}
	repl := []HypTurn{{Speaker: "B", Start: 50, End: 52}}

	m := MeasureDiarSplice(ref, base, 50, 52, repl)
	if m.DERDelta >= 0 {
		t.Errorf("recovering B must lower DER, got %v", m.DERDelta)
	}
	// ~2 s of error fixed over ~100 s speech ≈ 0.02 — well above the 0.0005 floor that
	// washes a single-word WER fix; so whole-meeting DER alone drives per-span accept.
	if math.Abs(m.DERDelta) < corebench.RepairMinImproveAbs {
		t.Errorf("a 2 s DER fix must clear the accept threshold, got |%v|", m.DERDelta)
	}
	out := m.Outcome(corebench.RepairSpan{StartSec: 50, EndSec: 52}, corebench.MethodAlternateDecode, 0.001)
	if !out.Accepted {
		t.Errorf("the small fix must be accepted: %+v", out)
	}
}
