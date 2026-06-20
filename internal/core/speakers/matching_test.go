package speakers

import (
	"math"
	"testing"
)

func TestCosineSimilarity(t *testing.T) {
	tests := []struct {
		name    string
		a       Embedding
		b       Embedding
		wantSim float64
		wantErr bool
	}{
		{
			name:    "identical vectors",
			a:       Embedding{1, 0, 0},
			b:       Embedding{1, 0, 0},
			wantSim: 1.0,
			wantErr: false,
		},
		{
			name:    "orthogonal vectors",
			a:       Embedding{1, 0, 0},
			b:       Embedding{0, 1, 0},
			wantSim: 0.0,
			wantErr: false,
		},
		{
			name:    "opposite vectors",
			a:       Embedding{1, 0, 0},
			b:       Embedding{-1, 0, 0},
			wantSim: -1.0,
			wantErr: false,
		},
		{
			name:    "dimension mismatch",
			a:       Embedding{1, 0, 0},
			b:       Embedding{1, 0},
			wantSim: 0,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := CosineSimilarity(tt.a, tt.b)
			if (err != nil) != tt.wantErr {
				t.Errorf("CosineSimilarity() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && got != tt.wantSim {
				t.Errorf("CosineSimilarity() = %v, want %v", got, tt.wantSim)
			}
		})
	}
}

func TestNormalize(t *testing.T) {
	tests := []struct {
		name     string
		input    Embedding
		expected []float64
	}{
		{
			name:     "unit vector unchanged",
			input:    Embedding{1, 0, 0},
			expected: []float64{1, 0, 0},
		},
		{
			name:     "non-unit normalized",
			input:    Embedding{3, 4},
			expected: []float64{0.6, 0.8},
		},
		{
			name:     "zero vector",
			input:    Embedding{0, 0, 0},
			expected: []float64{0, 0, 0},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Normalize(tt.input)
			for i := range got {
				if got[i] != tt.expected[i] {
					t.Errorf("Normalize()[%d] = %v, want %v", i, got[i], tt.expected[i])
				}
			}
		})
	}
}

func TestCentroid(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		got, err := Centroid(nil)
		if err != nil {
			t.Errorf("Centroid() error = %v", err)
		}
		if got != nil {
			t.Errorf("Centroid() = %v, want nil", got)
		}
	})

	t.Run("single embedding", func(t *testing.T) {
		embs := []Embedding{{1, 0, 0}}
		got, err := Centroid(embs)
		if err != nil {
			t.Errorf("Centroid() error = %v", err)
		}
		if len(got) != 3 {
			t.Errorf("Centroid() len = %d, want 3", len(got))
		}
	})

	t.Run("two orthogonal embeddings", func(t *testing.T) {
		embs := []Embedding{{1, 0, 0}, {0, 1, 0}}
		got, err := Centroid(embs)
		if err != nil {
			t.Errorf("Centroid() error = %v", err)
		}
		if len(got) != 3 {
			t.Errorf("Centroid() len = %d, want 3", len(got))
		}
	})

	t.Run("dimension mismatch", func(t *testing.T) {
		embs := []Embedding{{1, 0, 0}, {1, 0}}
		_, err := Centroid(embs)
		if err == nil {
			t.Errorf("Centroid() expected error for dimension mismatch")
		}
	})
}

func TestMatch(t *testing.T) {
	candidates := []Candidate{
		{ProfileID: "p1", Name: "Alice", Centroid: Normalize(Embedding{1, 0, 0})},
		{ProfileID: "p2", Name: "Bob", Centroid: Normalize(Embedding{0, 1, 0})},
	}

	tests := []struct {
		name             string
		query            Embedding
		wantStatus       MatchStatus
		wantProfileID    string
		wantScoreAtLeast float64
	}{
		{
			name:             "exact match alice",
			query:            Embedding{1, 0, 0},
			wantStatus:       StatusAuto,
			wantProfileID:    "p1",
			wantScoreAtLeast: 0.99,
		},
		{
			name:             "exact match bob",
			query:            Embedding{0, 1, 0},
			wantStatus:       StatusAuto,
			wantProfileID:    "p2",
			wantScoreAtLeast: 0.99,
		},
		{
			name:             "below threshold new speaker",
			query:            Embedding{0, 0, 1},
			wantStatus:       StatusNew,
			wantProfileID:    "",
			wantScoreAtLeast: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dec, err := Match(tt.query, candidates)
			if err != nil {
				t.Fatalf("Match() error = %v", err)
			}
			if dec.Status != tt.wantStatus {
				t.Errorf("Match() status = %v, want %v", dec.Status, tt.wantStatus)
			}
			if dec.Score < tt.wantScoreAtLeast {
				t.Errorf("Match() score = %v, want at least %v", dec.Score, tt.wantScoreAtLeast)
			}
		})
	}
}

func TestMatchDimensionMismatch(t *testing.T) {
	query := Embedding{1, 0}
	candidates := []Candidate{
		{ProfileID: "p1", Name: "Alice", Centroid: Embedding{1, 0, 0}},
	}
	_, err := Match(query, candidates)
	if err == nil {
		t.Errorf("Match() expected error for dimension mismatch")
	}
}

func TestMatchAmbiguousThreshold(t *testing.T) {
	alice := Normalize(Embedding{1, 0, 0})
	bob := Normalize(Embedding{0, 1, 0})
	query := Normalize(Embedding{0, 0, 1})
	dec, err := Match(query, []Candidate{
		{ProfileID: "p1", Name: "Alice", Centroid: alice},
		{ProfileID: "p2", Name: "Bob", Centroid: bob},
	})
	if err != nil {
		t.Fatalf("Match() error = %v", err)
	}
	if dec.Score >= 0.55 {
		t.Errorf("Match() score = %v, want < 0.55 for new speaker", dec.Score)
	}
	if dec.Status != StatusNew {
		t.Errorf("Match() status = %v, want StatusNew", dec.Status)
	}
}

func TestMatchEmptyCandidates(t *testing.T) {
	dec, err := Match(Embedding{1, 0, 0}, nil)
	if err != nil {
		t.Fatalf("Match() error = %v", err)
	}
	if dec.Status != StatusNew {
		t.Errorf("Match() with nil candidates status = %v, want StatusNew", dec.Status)
	}

	dec, err = Match(Embedding{1, 0, 0}, []Candidate{})
	if err != nil {
		t.Fatalf("Match() error = %v", err)
	}
	if dec.Status != StatusNew {
		t.Errorf("Match() with empty candidates status = %v, want StatusNew", dec.Status)
	}
}

func TestMatchEmptyQuery(t *testing.T) {
	candidates := []Candidate{{ProfileID: "p1", Name: "Alice", Centroid: Embedding{1, 0, 0}}}
	dec, err := Match(Embedding{}, candidates)
	if err != nil {
		t.Fatalf("Match() error = %v", err)
	}
	if dec.Status != StatusNew {
		t.Errorf("Match() with empty query status = %v, want StatusNew", dec.Status)
	}
}

func TestMatchWithDuration(t *testing.T) {
	candidates := []Candidate{
		{ProfileID: "p1", Name: "Alice", Centroid: Embedding{1, 0, 0}},
	}

	t.Run("short segment rejected", func(t *testing.T) {
		query := EmbeddingWithDuration{Embedding: Embedding{1, 0, 0}, Duration: 0.5}
		_, err := MatchWithDuration(query, candidates)
		if err == nil {
			t.Errorf("MatchWithDuration() expected error for short segment")
		}
	})

	t.Run("valid segment matched", func(t *testing.T) {
		query := EmbeddingWithDuration{Embedding: Embedding{1, 0, 0}, Duration: 2.0}
		dec, err := MatchWithDuration(query, candidates)
		if err != nil {
			t.Fatalf("MatchWithDuration() error = %v", err)
		}
		if dec.Status != StatusAuto {
			t.Errorf("MatchWithDuration() status = %v, want StatusAuto", dec.Status)
		}
	})
}

func TestDimensionMismatch(t *testing.T) {
	query := Embedding{1, 0}
	candidates := []Candidate{
		{ProfileID: "p1", Name: "Alice", Centroid: Embedding{1, 0, 0}},
	}
	_, err := Match(query, candidates)
	if err == nil {
		t.Errorf("Match() expected error for dimension mismatch")
	}
}

// unitWithX returns a normalized 3-d vector whose cosine with the unit query
// {1,0,0} is exactly x, making it easy to dial a candidate's match score.
func unitWithX(x float64) Embedding {
	y := math.Sqrt(1 - x*x)
	return Normalize(Embedding{x, y, 0})
}

func TestRankCandidates(t *testing.T) {
	query := Embedding{1, 0, 0}
	cands := []Candidate{
		{ProfileID: "p1", Name: "Alice", Centroid: unitWithX(0.40)},
		{ProfileID: "p2", Name: "Bob", Centroid: unitWithX(0.90)},
		{ProfileID: "p3", Name: "Carol", Centroid: unitWithX(0.62)},
	}

	t.Run("sorted desc with status", func(t *testing.T) {
		got, err := RankCandidates(query, cands, 0)
		if err != nil {
			t.Fatalf("RankCandidates: %v", err)
		}
		if len(got) != 3 {
			t.Fatalf("got %d decisions, want 3", len(got))
		}
		if got[0].ProfileID != "p2" || got[1].ProfileID != "p3" || got[2].ProfileID != "p1" {
			t.Errorf("order = %s,%s,%s, want p2,p3,p1", got[0].ProfileID, got[1].ProfileID, got[2].ProfileID)
		}
		// scores strictly descending
		for i := 1; i < len(got); i++ {
			if got[i].Score > got[i-1].Score {
				t.Errorf("not sorted desc at %d", i)
			}
		}
		// status reflects bands: 0.90 auto, 0.62 pending, 0.40 new
		if got[0].Status != StatusAuto || got[1].Status != StatusPending || got[2].Status != StatusNew {
			t.Errorf("statuses = %s,%s,%s", got[0].Status, got[1].Status, got[2].Status)
		}
	})

	t.Run("topK caps", func(t *testing.T) {
		got, err := RankCandidates(query, cands, 2)
		if err != nil {
			t.Fatalf("RankCandidates: %v", err)
		}
		if len(got) != 2 || got[0].ProfileID != "p2" {
			t.Errorf("topK=2 = %+v", got)
		}
	})

	t.Run("empty", func(t *testing.T) {
		got, err := RankCandidates(query, nil, 3)
		if err != nil || got != nil {
			t.Errorf("empty candidates = %v, %v", got, err)
		}
	})
}

func TestMatchConfidentMarginGate(t *testing.T) {
	query := Embedding{1, 0, 0}

	t.Run("ambiguous top-2 downgraded to pending", func(t *testing.T) {
		cands := []Candidate{
			{ProfileID: "p1", Name: "Alice", Centroid: unitWithX(0.72)},
			{ProfileID: "p2", Name: "Bob", Centroid: unitWithX(0.70)},
		}
		// Plain Match (no margin) auto-confirms the 0.72 leader.
		plain, err := Match(query, cands)
		if err != nil {
			t.Fatalf("Match() error = %v", err)
		}
		if plain.Status != StatusAuto {
			t.Fatalf("Match() status = %v, want StatusAuto (no margin)", plain.Status)
		}
		// MatchConfident sees the 0.02 lead < 0.05 margin and holds for review.
		conf, err := MatchConfident(query, cands)
		if err != nil {
			t.Fatalf("MatchConfident() error = %v", err)
		}
		if conf.Status != StatusPending {
			t.Errorf("MatchConfident() status = %v, want StatusPending (within margin)", conf.Status)
		}
		if conf.ProfileID != "p1" {
			t.Errorf("MatchConfident() profile = %v, want p1 (still the leader)", conf.ProfileID)
		}
	})

	t.Run("clear leader stays auto", func(t *testing.T) {
		cands := []Candidate{
			{ProfileID: "p1", Name: "Alice", Centroid: unitWithX(0.85)},
			{ProfileID: "p2", Name: "Bob", Centroid: unitWithX(0.60)},
		}
		conf, err := MatchConfident(query, cands)
		if err != nil {
			t.Fatalf("MatchConfident() error = %v", err)
		}
		if conf.Status != StatusAuto {
			t.Errorf("MatchConfident() status = %v, want StatusAuto (0.25 lead)", conf.Status)
		}
	})

	t.Run("lone candidate not penalized by margin", func(t *testing.T) {
		cands := []Candidate{{ProfileID: "p1", Name: "Alice", Centroid: unitWithX(0.90)}}
		conf, err := MatchConfident(query, cands)
		if err != nil {
			t.Fatalf("MatchConfident() error = %v", err)
		}
		if conf.Status != StatusAuto {
			t.Errorf("MatchConfident() status = %v, want StatusAuto (single candidate)", conf.Status)
		}
	})

	t.Run("margin only gates auto, not pending", func(t *testing.T) {
		// Two close candidates both in the pending band stay pending — the gate
		// must never upgrade, only withhold an auto.
		cands := []Candidate{
			{ProfileID: "p1", Name: "Alice", Centroid: unitWithX(0.60)},
			{ProfileID: "p2", Name: "Bob", Centroid: unitWithX(0.59)},
		}
		conf, err := MatchConfident(query, cands)
		if err != nil {
			t.Fatalf("MatchConfident() error = %v", err)
		}
		if conf.Status != StatusPending {
			t.Errorf("MatchConfident() status = %v, want StatusPending", conf.Status)
		}
	})
}

func TestMatchWithConfigThresholds(t *testing.T) {
	query := Embedding{1, 0, 0}
	cands := []Candidate{{ProfileID: "p1", Name: "Alice", Centroid: unitWithX(0.62)}}

	// A 0.62 score is pending under shipped thresholds...
	def, err := MatchWithConfig(query, cands, DefaultMatchConfig())
	if err != nil {
		t.Fatalf("MatchWithConfig() error = %v", err)
	}
	if def.Status != StatusPending {
		t.Errorf("default thresholds: status = %v, want StatusPending", def.Status)
	}
	// ...but auto under a lowered config, proving thresholds are configurable.
	low, err := MatchWithConfig(query, cands, MatchConfig{Auto: 0.58, Pending: 0.50})
	if err != nil {
		t.Fatalf("MatchWithConfig() error = %v", err)
	}
	if low.Status != StatusAuto {
		t.Errorf("lowered thresholds: status = %v, want StatusAuto", low.Status)
	}
}

func TestMatchCandidates(t *testing.T) {
	candidates := []Candidate{
		{ProfileID: "p1", Name: "Alice", Centroid: Embedding{1, 0, 0}},
		{ProfileID: "p2", Name: "Bob", Centroid: Embedding{0, 1, 0}},
	}

	t.Run("returns all decisions", func(t *testing.T) {
		decisions, err := MatchCandidates(Embedding{1, 0, 0}, candidates)
		if err != nil {
			t.Fatalf("MatchCandidates() error = %v", err)
		}
		if len(decisions) != 2 {
			t.Errorf("MatchCandidates() returned %d decisions, want 2", len(decisions))
		}
	})

	t.Run("skips dimension mismatches", func(t *testing.T) {
		query := Embedding{1, 0}
		cands := []Candidate{
			{ProfileID: "p1", Name: "Alice", Centroid: Embedding{1, 0}},
			{ProfileID: "p2", Name: "Bob", Centroid: Embedding{0, 1}},
			{ProfileID: "p3", Name: "Carol", Centroid: Embedding{1, 0, 0}},
		}
		decisions, err := MatchCandidates(query, cands)
		if err != nil {
			t.Fatalf("MatchCandidates() error = %v", err)
		}
		if len(decisions) != 2 {
			t.Errorf("MatchCandidates() returned %d decisions, want 2 (skipped mismatched)", len(decisions))
		}
	})

	t.Run("empty query returns nil", func(t *testing.T) {
		decisions, err := MatchCandidates(Embedding{}, candidates)
		if err != nil {
			t.Fatalf("MatchCandidates() error = %v", err)
		}
		if decisions != nil {
			t.Errorf("MatchCandidates() with empty query = %v, want nil", decisions)
		}
	})
}

func TestRunningMean_DeweightsAsCountGrows(t *testing.T) {
	// A profile enrolled toward direction A. A new, noisy embedding pulls toward
	// B. With a large prior count the centroid should barely move; with count 1 it
	// should move much more (the old 50/50 behavior).
	a := Embedding{1, 0}
	b := Embedding{0, 1}

	near, err := RunningMean(a, 100, b)
	if err != nil {
		t.Fatalf("RunningMean count=100: %v", err)
	}
	simNearA, _ := CosineSimilarity(near, a)

	far, err := RunningMean(a, 1, b)
	if err != nil {
		t.Fatalf("RunningMean count=1: %v", err)
	}
	simFarA, _ := CosineSimilarity(far, a)

	// A high prior count keeps the centroid much closer to A than a single-sample
	// update does — that's the whole point of tracking the count.
	if simNearA <= simFarA {
		t.Errorf("count=100 should stay closer to A than count=1: near=%.4f far=%.4f", simNearA, simFarA)
	}
	// count=1 is the symmetric two-sample case: equidistant from A and B.
	simFarB, _ := CosineSimilarity(far, b)
	if math.Abs(simFarA-simFarB) > 1e-9 {
		t.Errorf("count=1 should be equidistant from A and B: A=%.4f B=%.4f", simFarA, simFarB)
	}
}

func TestRunningMean_ClampsAndValidates(t *testing.T) {
	// count < 1 is treated as 1 (single prior enrollment).
	got0, err := RunningMean(Embedding{1, 0}, 0, Embedding{0, 1})
	if err != nil {
		t.Fatalf("count=0: %v", err)
	}
	got1, _ := RunningMean(Embedding{1, 0}, 1, Embedding{0, 1})
	for i := range got0 {
		if math.Abs(got0[i]-got1[i]) > 1e-9 {
			t.Errorf("count=0 should behave as count=1: %v vs %v", got0, got1)
		}
	}
	// Dimension mismatch is an error, not a panic.
	if _, err := RunningMean(Embedding{1, 0}, 5, Embedding{1, 0, 0}); err == nil {
		t.Error("expected dimension-mismatch error")
	}
}

func TestWeightedMean_FavorsHeavierSide(t *testing.T) {
	a := Embedding{1, 0}
	b := Embedding{0, 1}

	// Merging a 1-weight profile into a 20-weight one should leave the result
	// dominated by A (the heavier side), not a 50/50 split.
	heavy, err := WeightedMean(a, 20, b, 1)
	if err != nil {
		t.Fatalf("WeightedMean: %v", err)
	}
	simA, _ := CosineSimilarity(heavy, a)
	simB, _ := CosineSimilarity(heavy, b)
	if simA <= simB {
		t.Errorf("20:1 merge should stay closer to A: A=%.4f B=%.4f", simA, simB)
	}

	// Equal weights are the symmetric midpoint.
	even, _ := WeightedMean(a, 3, b, 3)
	evenA, _ := CosineSimilarity(even, a)
	evenB, _ := CosineSimilarity(even, b)
	if math.Abs(evenA-evenB) > 1e-9 {
		t.Errorf("equal weights should be equidistant: A=%.4f B=%.4f", evenA, evenB)
	}

	// Negative weight clamps to 0 (the other side wins entirely).
	clamped, _ := WeightedMean(a, -5, b, 2)
	if cb, _ := CosineSimilarity(clamped, b); cb < 0.999 {
		t.Errorf("negative weight should clamp to 0, leaving B: cos=%.4f", cb)
	}

	if _, err := WeightedMean(Embedding{1, 0}, 1, Embedding{1, 0, 0}, 1); err == nil {
		t.Error("expected dimension-mismatch error")
	}
}
