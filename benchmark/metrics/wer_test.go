package metrics

import "testing"

func TestWER(t *testing.T) {
	cases := []struct {
		name          string
		ref, hyp      []string
		sub, del, ins int
		refLen        int
		rate          float64
	}{
		{"identical", []string{"the", "cat", "sat"}, []string{"the", "cat", "sat"}, 0, 0, 0, 3, 0},
		{"substitution", []string{"the", "cat", "sat"}, []string{"the", "cat", "sit"}, 1, 0, 0, 3, 1.0 / 3},
		{"deletion", []string{"the", "cat", "sat"}, []string{"the", "sat"}, 0, 1, 0, 3, 1.0 / 3},
		{"insertion", []string{"the", "cat"}, []string{"the", "big", "cat"}, 0, 0, 1, 2, 1.0 / 2},
		{"sub+ins", []string{"the", "cat", "sat", "on"}, []string{"the", "cat", "sit", "on", "mat"}, 1, 0, 1, 4, 0.5},
		{"empty both", nil, nil, 0, 0, 0, 0, 0},
		{"empty ref", nil, []string{"a"}, 0, 0, 1, 0, 1.0},
		{"empty hyp", []string{"a", "b"}, nil, 0, 2, 0, 2, 1.0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := WER(c.ref, c.hyp)
			if got.Sub != c.sub || got.Del != c.del || got.Ins != c.ins {
				t.Errorf("S/D/I = %d/%d/%d, want %d/%d/%d", got.Sub, got.Del, got.Ins, c.sub, c.del, c.ins)
			}
			if got.RefLen != c.refLen {
				t.Errorf("RefLen = %d, want %d", got.RefLen, c.refLen)
			}
			approx(t, "Rate", got.Rate, c.rate)
		})
	}
}

func TestWERText(t *testing.T) {
	got := WERText("Ship v1!", "ship V1")
	if got.Errors() != 0 {
		t.Errorf("normalized identical should be 0 errors, got %+v", got)
	}
}

func TestAlignHyp(t *testing.T) {
	cases := []struct {
		name     string
		ref, hyp []string
		want     []bool // one label per hyp token, true = matched
	}{
		{"identical", []string{"the", "cat", "sat"}, []string{"the", "cat", "sat"}, []bool{true, true, true}},
		{"substitution", []string{"the", "cat", "sat"}, []string{"the", "cat", "sit"}, []bool{true, true, false}},
		// deletion consumes only a ref token: every surviving hyp word matched.
		{"deletion", []string{"the", "cat", "sat"}, []string{"the", "sat"}, []bool{true, true}},
		{"insertion", []string{"the", "cat"}, []string{"the", "big", "cat"}, []bool{true, false, true}},
		{"sub+ins", []string{"the", "cat", "sat", "on"}, []string{"the", "cat", "sit", "on", "mat"}, []bool{true, true, false, true, false}},
		{"empty hyp", []string{"a", "b"}, nil, []bool{}},
		{"all wrong vs empty ref", nil, []string{"a", "b"}, []bool{false, false}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := AlignHyp(c.ref, c.hyp)
			if len(got) != len(c.hyp) {
				t.Fatalf("len(labels) = %d, want %d (one per hyp token)", len(got), len(c.hyp))
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Errorf("label[%d] (%q) = %v, want %v (full %v)", i, c.hyp[i], got[i], c.want[i], got)
				}
			}
			// The false-count must equal WER's substitution+insertion total — the
			// labels are a partition of the same backtrace.
			r := WER(c.ref, c.hyp)
			falses := 0
			for _, ok := range got {
				if !ok {
					falses++
				}
			}
			if falses != r.Sub+r.Ins {
				t.Errorf("false labels = %d, want Sub+Ins = %d", falses, r.Sub+r.Ins)
			}
		})
	}
}

func TestEditDistance(t *testing.T) {
	cases := []struct {
		a, b []string
		want int
	}{
		{[]string{"a", "b", "c"}, []string{"a", "b", "c"}, 0},
		{[]string{"a", "b", "c"}, []string{"a", "x", "c"}, 1},
		{nil, []string{"a", "b"}, 2},
		{[]string{"a", "b"}, nil, 2},
		{[]string{"a", "b", "c", "d"}, []string{"a", "b"}, 2},
	}
	for _, c := range cases {
		if got := editDistance(c.a, c.b); got != c.want {
			t.Errorf("editDistance(%v,%v) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}
