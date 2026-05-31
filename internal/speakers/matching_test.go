package speakers

import (
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
