package entityrepair

import "testing"

func words(texts ...string) []Word {
	out := make([]Word, len(texts))
	for i, t := range texts {
		out[i] = Word{Text: t}
	}
	return out
}

func conf(v float64) *float64 { return &v }

func TestApply_SnapsCloseSingleTokenEntity(t *testing.T) {
	in := words("the", "kubernates", "cluster") // misspelled "kubernetes"
	out, reps := Apply(in, []string{"Kubernetes"}, DefaultOptions())
	if out[1] != "Kubernetes" {
		t.Fatalf("expected snap to Kubernetes, got %q", out[1])
	}
	if len(reps) != 1 || reps[0].Index != 1 || reps[0].From != "kubernates" || reps[0].To != "Kubernetes" {
		t.Errorf("provenance wrong: %+v", reps)
	}
	if out[0] != "the" || out[2] != "cluster" {
		t.Error("surrounding words must be untouched")
	}
}

func TestApply_LeavesExactMatchAlone(t *testing.T) {
	in := words("deploy", "Kubernetes", "now")
	_, reps := Apply(in, []string{"Kubernetes"}, DefaultOptions())
	if len(reps) != 0 {
		t.Errorf("an exact match must not be repaired: %+v", reps)
	}
}

func TestApply_MatchesCaseAndPunctuationOnlyAsExact(t *testing.T) {
	in := words("hey", "kubernetes,", "ok") // differs only by case/punct → normalized exact
	_, reps := Apply(in, []string{"Kubernetes"}, DefaultOptions())
	if len(reps) != 0 {
		t.Errorf("case/punctuation-only difference is exact after normalize, must not repair: %+v", reps)
	}
}

func TestApply_RejectsLowSimilarity(t *testing.T) {
	in := words("the", "elephant", "ran")
	_, reps := Apply(in, []string{"Kubernetes"}, DefaultOptions())
	if len(reps) != 0 {
		t.Errorf("a far word must not be snapped to an entity: %+v", reps)
	}
}

func TestApply_ProtectsRealWordBelowBar(t *testing.T) {
	// "mark" (a real common word) vs the name "Marc": one edit in four chars → 0.75
	// similarity, under the 0.80 bar, so it is NOT rewritten. This is the false-positive
	// the high bar exists to prevent.
	in := words("ask", "mark", "about")
	_, reps := Apply(in, []string{"Marc"}, DefaultOptions())
	if len(reps) != 0 {
		t.Errorf("a real word below the similarity bar must be left alone: %+v", reps)
	}
}

func TestApply_MinTermLenGate(t *testing.T) {
	// "kubernates" would snap to "Kubernetes", but a MinTermLen above the term length
	// makes the term ineligible — the distinctiveness guard, isolated.
	in := words("the", "kubernates", "cluster")
	opts := DefaultOptions()
	opts.MinTermLen = 12 // "kubernetes" is 10 normalized chars → ineligible
	if _, reps := Apply(in, []string{"Kubernetes"}, opts); len(reps) != 0 {
		t.Errorf("a term under MinTermLen must be ineligible: %+v", reps)
	}
}

func TestApply_MultiTokenEntity(t *testing.T) {
	in := words("met", "lucas", "strickler", "today") // ~ "Lukas Strickler"
	out, reps := Apply(in, []string{"Lukas Strickler"}, DefaultOptions())
	if out[1] != "Lukas" || out[2] != "Strickler" {
		t.Fatalf("expected multi-token snap, got %q %q", out[1], out[2])
	}
	if len(reps) != 1 || reps[0].Length != 2 {
		t.Errorf("expected one 2-word repair: %+v", reps)
	}
}

func TestApply_ConfidenceGateProtectsConfidentWords(t *testing.T) {
	in := []Word{{Text: "the"}, {Text: "kubernates", Confidence: conf(0.98)}, {Text: "cluster"}}
	opts := DefaultOptions()
	opts.LowConfidence = func(c *float64) bool { return c != nil && *c <= 0.5 }
	_, reps := Apply(in, []string{"Kubernetes"}, opts)
	if len(reps) != 0 {
		t.Errorf("a confident word must be protected when a confidence gate is set: %+v", reps)
	}
	// Same word, now low confidence → repaired.
	in[1].Confidence = conf(0.2)
	out, reps := Apply(in, []string{"Kubernetes"}, opts)
	if len(reps) != 1 || out[1] != "Kubernetes" {
		t.Errorf("a low-confidence near-miss should be repaired: out=%v reps=%+v", out, reps)
	}
}

func TestApply_InertOnEmptyInputs(t *testing.T) {
	if out, reps := Apply(nil, []string{"X"}, DefaultOptions()); reps != nil || len(out) != 0 {
		t.Error("empty words → no repairs")
	}
	if _, reps := Apply(words("a", "b"), nil, DefaultOptions()); reps != nil {
		t.Error("empty terms → no repairs")
	}
	if _, reps := Apply(words("a", "b"), []string{"Kubernetes"}, Options{}); reps != nil {
		t.Error("zero-value options (MinSimilarity 0) must be inert, not match-everything")
	}
}

func TestApply_MergesSplitEntity(t *testing.T) {
	// The recognizer split "Datadog" into two words — rejoin them.
	in := words("we", "use", "data", "dog", "daily")
	out, reps := Apply(in, []string{"Datadog"}, DefaultOptions())
	if len(reps) != 1 || reps[0].Index != 2 || reps[0].Length != 2 {
		t.Fatalf("expected one 2-word merge at index 2, got %+v", reps)
	}
	if out[2] != "Datadog" || out[3] != "" {
		t.Errorf("merge should write the canonical token and blank the extra, got %q %q", out[2], out[3])
	}
}

func TestApply_PrefersEqualCountOverMerge(t *testing.T) {
	// "kubernates" alone is a clean near-miss; it must be fixed in place, never merged
	// with the following word.
	in := words("the", "kubernates", "cluster")
	_, reps := Apply(in, []string{"Kubernetes"}, DefaultOptions())
	if len(reps) != 1 || reps[0].Length != 1 {
		t.Errorf("expected a single-word fix, not a merge: %+v", reps)
	}
}

func TestApply_DoesNotMergeUnrelatedWords(t *testing.T) {
	in := words("the", "data", "is", "good")
	_, reps := Apply(in, []string{"Datadog"}, DefaultOptions())
	if len(reps) != 0 {
		t.Errorf("'data is' must not be merged into Datadog: %+v", reps)
	}
}

func TestApply_DoesNotDoubleApplyOverlappingWindows(t *testing.T) {
	in := words("foo", "kubernates", "kubernates", "bar")
	out, reps := Apply(in, []string{"Kubernetes"}, DefaultOptions())
	if len(reps) != 2 {
		t.Fatalf("expected each near-miss repaired once, got %+v", reps)
	}
	if out[1] != "Kubernetes" || out[2] != "Kubernetes" {
		t.Errorf("both should snap: %v", out)
	}
}
