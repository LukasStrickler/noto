package speakers

import (
	"fmt"

	"github.com/lukasstrickler/noto/internal/core/notoerr"
)

// DimensionMismatchError indicates embedding vector dimension mismatch.
type DimensionMismatchError struct {
	Expected int
	Got      int
}

func (e *DimensionMismatchError) Error() string {
	return fmt.Sprintf("embedding dimension mismatch: expected %d, got %d", e.Expected, e.Got)
}

func (e *DimensionMismatchError) Code() string { return "dimension_mismatch" }

// ErrDimensionMismatch is shorthand for creating a dimension mismatch error.
func ErrDimensionMismatch(expected, got int) *DimensionMismatchError {
	return &DimensionMismatchError{Expected: expected, Got: got}
}

// Embedding is a speaker voice embedding vector.
// Values should be normalized before similarity computations.
type Embedding []float64

// EmbeddingWithDuration pairs an embedding with its source segment duration.
type EmbeddingWithDuration struct {
	Embedding Embedding
	Duration  float64 // seconds
}

// MinDuration is the minimum segment duration (1.5 sec) required for reliable embedding.
const MinDuration = 1.5

// IsShort returns true if the segment is too short for reliable matching.
func (e *EmbeddingWithDuration) IsShort() bool {
	return e.Duration < MinDuration
}

// Candidate is a stored speaker profile with centroid embedding.
type Candidate struct {
	ProfileID string
	Name      string
	Centroid  Embedding
}

// MatchStatus indicates the result of a speaker match decision.
type MatchStatus string

const (
	StatusAuto    MatchStatus = "auto"    // >= 0.70 score
	StatusPending MatchStatus = "pending" // >= 0.55 && < 0.70 score
	StatusNew     MatchStatus = "new"     // < 0.55 score
)

// MatchDecision is the structured result of a speaker match operation.
type MatchDecision struct {
	ProfileID string      `json:"profile_id"`
	Name      string      `json:"name"`
	Score     float64     `json:"score"`
	Status    MatchStatus `json:"status"`
	Reason    string      `json:"reason"`
}

// AutoThreshold is the cosine similarity threshold for auto-confirmed match.
const AutoThreshold = 0.70

// PendingThreshold is the cosine similarity threshold below which a new speaker is declared.
const PendingThreshold = 0.55

// DimError returns a structured dimension mismatch error wrapped in notoerr.Error.
func DimError(expected, got int) *notoerr.Error {
	return notoerr.New("dimension_mismatch",
		fmt.Sprintf("embedding dimension mismatch: expected %d, got %d", expected, got),
		map[string]any{"expected": expected, "got": got},
	)
}
