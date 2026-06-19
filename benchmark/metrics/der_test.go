package metrics

import "testing"

var noCollar = DEROptions{Collar: 0, SkipOverlap: false}

func TestDERPerfect(t *testing.T) {
	ref := []Segment{{"A", 0, 10}}
	hyp := []Segment{{"X", 0, 10}}
	got := DER(ref, hyp, noCollar)
	approx(t, "Rate", got.Rate, 0)
	approx(t, "RefSpeech", got.RefSpeech, 10)
}

func TestDERPureMiss(t *testing.T) {
	ref := []Segment{{"A", 0, 10}}
	got := DER(ref, nil, noCollar)
	approx(t, "Missed", got.Missed, 10)
	approx(t, "Rate", got.Rate, 1.0)
}

func TestDERPureFalseAlarm(t *testing.T) {
	ref := []Segment{{"A", 0, 5}}
	hyp := []Segment{{"X", 0, 10}}
	got := DER(ref, hyp, noCollar)
	approx(t, "FalseAlarm", got.FalseAlarm, 5)
	approx(t, "RefSpeech", got.RefSpeech, 5)
	approx(t, "Rate", got.Rate, 1.0)
}

func TestDERSwapForgivenByMapping(t *testing.T) {
	// Hypothesis labels are the opposite of the natural reading, but the optimal
	// mapping recovers them → zero confusion.
	ref := []Segment{{"A", 0, 5}, {"B", 5, 10}}
	hyp := []Segment{{"Y", 0, 5}, {"X", 5, 10}}
	got := DER(ref, hyp, noCollar)
	approx(t, "Rate", got.Rate, 0)
}

func TestDERConfusion(t *testing.T) {
	// One hypothesis speaker covers two reference speakers → half is confusion.
	ref := []Segment{{"A", 0, 5}, {"B", 5, 10}}
	hyp := []Segment{{"X", 0, 10}}
	got := DER(ref, hyp, noCollar)
	approx(t, "Confusion", got.Confusion, 5)
	approx(t, "RefSpeech", got.RefSpeech, 10)
	approx(t, "Rate", got.Rate, 0.5)
}

func TestDERCollarForgivesBoundaryError(t *testing.T) {
	// A short false alarm just past the reference end boundary: scored without a
	// collar, forgiven entirely with the default 0.25 s collar.
	ref := []Segment{{"A", 0, 10}}
	hyp := []Segment{{"A", 0, 10}, {"B", 10, 10.2}}

	without := DER(ref, hyp, noCollar)
	if without.Rate <= 0 {
		t.Fatalf("expected nonzero DER without collar, got %v", without.Rate)
	}
	approx(t, "FalseAlarm(noCollar)", without.FalseAlarm, 0.2)

	with := DER(ref, hyp, DefaultDEROptions())
	approx(t, "FalseAlarm(collar)", with.FalseAlarm, 0)
	approx(t, "Rate(collar)", with.Rate, 0)
}

func TestDERSkipOverlap(t *testing.T) {
	// Reference has overlap [5,10]; hypothesis misses speaker B there.
	ref := []Segment{{"A", 0, 10}, {"B", 5, 10}}
	hyp := []Segment{{"A", 0, 10}}

	scored := DER(ref, hyp, noCollar)
	approx(t, "Rate(scored overlap)", scored.Rate, 5.0/15.0)

	skipped := DER(ref, hyp, DEROptions{SkipOverlap: true})
	approx(t, "Rate(skip overlap)", skipped.Rate, 0)
}
