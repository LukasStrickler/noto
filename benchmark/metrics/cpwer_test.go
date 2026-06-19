package metrics

import "testing"

func TestCpWERSwapForgiven(t *testing.T) {
	// Anonymous hypothesis labels are swapped relative to the reference; cpWER's
	// permutation search forgives it.
	ref := map[string][]string{"A": {"hello", "world"}, "B": {"foo", "bar"}}
	hyp := map[string][]string{"X": {"foo", "bar"}, "Y": {"hello", "world"}}
	got := CpWER(ref, hyp)
	approx(t, "Rate", got.Rate, 0)
	if got.RefLen != 4 {
		t.Errorf("RefLen = %d, want 4", got.RefLen)
	}
}

func TestCpWERTranscriptionError(t *testing.T) {
	ref := map[string][]string{"A": {"the", "cat", "sat"}}
	hyp := map[string][]string{"A": {"the", "cat", "sit"}}
	got := CpWER(ref, hyp)
	approx(t, "Rate", got.Rate, 1.0/3)
}

func TestCpWERSplitSpeakerPenalized(t *testing.T) {
	// All four words are correct but split across two hypothesis speakers; cpWER
	// can match only one of them to the single reference speaker → 50% errors.
	ref := map[string][]string{"A": {"a", "b", "c", "d"}}
	hyp := map[string][]string{"X": {"a", "b"}, "Y": {"c", "d"}}
	got := CpWER(ref, hyp)
	if got.Errors() != 4 || got.RefLen != 4 {
		t.Errorf("got %+v, want 4 errors over 4 ref tokens", got)
	}
	approx(t, "Rate", got.Rate, 1.0)
}

func TestCpWEREmpty(t *testing.T) {
	got := CpWER(map[string][]string{}, map[string][]string{})
	approx(t, "Rate", got.Rate, 0)
}

func TestSAWERWrongMappingPenalized(t *testing.T) {
	ref := map[string][]string{"A": {"hello", "world"}, "B": {"foo", "bar"}}
	hyp := map[string][]string{"X": {"foo", "bar"}, "Y": {"hello", "world"}}

	// Correct attribution: X is really B, Y is really A.
	right := SAWER(ref, hyp, map[string]string{"X": "B", "Y": "A"})
	approx(t, "Rate(correct mapping)", right.Rate, 0)

	// Wrong attribution: words land on the wrong person.
	wrong := SAWER(ref, hyp, map[string]string{"X": "A", "Y": "B"})
	approx(t, "Rate(wrong mapping)", wrong.Rate, 1.0)
}

func TestSAWERUnattributedIsInsertion(t *testing.T) {
	ref := map[string][]string{"A": {"x"}}
	hyp := map[string][]string{"A": {"x"}, "B": {"y"}}
	got := SAWER(ref, hyp, map[string]string{"A": "A"}) // B unmapped
	if got.Ins != 1 || got.RefLen != 1 {
		t.Errorf("got %+v, want 1 insertion over 1 ref token", got)
	}
	approx(t, "Rate", got.Rate, 1.0)
}
