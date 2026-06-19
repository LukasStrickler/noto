package speakers

import (
	"math"
	"sort"

	"github.com/lukasstrickler/noto/internal/core/notoerr"
)

func Normalize(v Embedding) Embedding {
	norm := math.Sqrt(Dot(v, v))
	if norm == 0 {
		return v
	}
	result := make(Embedding, len(v))
	for i, val := range v {
		result[i] = val / norm
	}
	return result
}

func Dot(a, b Embedding) float64 {
	if len(a) != len(b) {
		panic(ErrDimensionMismatch(len(a), len(b)))
	}
	var sum float64
	for i := range a {
		sum += a[i] * b[i]
	}
	return sum
}

func CosineSimilarity(a, b Embedding) (float64, error) {
	if len(a) != len(b) {
		return 0, DimError(len(a), len(b))
	}
	aNorm := Normalize(a)
	bNorm := Normalize(b)
	return Dot(aNorm, bNorm), nil
}

func Centroid(embeddings []Embedding) (Embedding, error) {
	if len(embeddings) == 0 {
		return nil, nil
	}
	dim := len(embeddings[0])
	for _, emb := range embeddings[1:] {
		if len(emb) != dim {
			return nil, DimError(dim, len(emb))
		}
	}
	result := make(Embedding, dim)
	for _, emb := range embeddings {
		norm := Normalize(emb)
		for i, val := range norm {
			result[i] += val
		}
	}
	count := float64(len(embeddings))
	for i := range result {
		result[i] /= count
	}
	return Normalize(result), nil
}

// MatchConfig tunes the decision boundaries. Margin is the minimum lead the
// top candidate must hold over the runner-up to AUTO-confirm: when two stored
// profiles are within Margin of each other the query is ambiguous (the
// same-gender confusion seen in the AMI persona bench), so we downgrade an
// otherwise-automatic match to pending for human review instead of silently
// merging into the wrong profile. Margin 0 preserves legacy behavior.
type MatchConfig struct {
	Auto    float64
	Pending float64
	Margin  float64
}

// DefaultAutoMargin is the guardrail used on the live matching path. Kept small
// so it only catches genuinely ambiguous top-2s, not ordinary matches.
const DefaultAutoMargin = 0.05

// DefaultMatchConfig mirrors the shipped thresholds with no margin, so Match
// behaves exactly as before.
func DefaultMatchConfig() MatchConfig {
	return MatchConfig{Auto: AutoThreshold, Pending: PendingThreshold, Margin: 0}
}

// Match keeps the original behavior (no margin gate) for existing callers.
func Match(query Embedding, candidates []Candidate) (MatchDecision, error) {
	return MatchWithConfig(query, candidates, DefaultMatchConfig())
}

// MatchConfident is the recommended live-path matcher: shipped thresholds plus
// the ambiguity margin, so a close call between two profiles never auto-merges.
func MatchConfident(query Embedding, candidates []Candidate) (MatchDecision, error) {
	cfg := DefaultMatchConfig()
	cfg.Margin = DefaultAutoMargin
	return MatchWithConfig(query, candidates, cfg)
}

// MatchWithConfig classifies query against candidates under cfg, returning the
// best candidate plus an auto/pending/new status. With cfg.Margin > 0 an
// otherwise-auto match whose lead over the second-best is below the margin is
// downgraded to pending.
func MatchWithConfig(query Embedding, candidates []Candidate, cfg MatchConfig) (MatchDecision, error) {
	if len(query) == 0 {
		return MatchDecision{Status: StatusNew, Reason: "empty query embedding"}, nil
	}
	if len(candidates) == 0 {
		return MatchDecision{Status: StatusNew, Reason: "no candidates provided"}, nil
	}
	queryNorm := Normalize(query)
	bestScore, secondScore := math.Inf(-1), math.Inf(-1)
	var bestCandidate *Candidate

	for i := range candidates {
		cand := &candidates[i]
		if len(cand.Centroid) != len(query) {
			return MatchDecision{}, DimError(len(query), len(cand.Centroid))
		}
		score := Dot(queryNorm, cand.Centroid)
		if score > bestScore {
			secondScore = bestScore
			bestScore, bestCandidate = score, cand
		} else if score > secondScore {
			secondScore = score
		}
	}

	if bestCandidate == nil {
		return MatchDecision{Status: StatusNew, Reason: "no matching candidate found"}, nil
	}

	var status MatchStatus
	var reason string
	switch {
	case bestScore >= cfg.Auto:
		status = StatusAuto
		reason = "auto match"
	case bestScore >= cfg.Pending:
		status = StatusPending
		reason = "pending confirmation"
	default:
		status = StatusNew
		reason = "below threshold"
	}

	// Ambiguity guardrail: a confident auto-merge requires a clear lead over the
	// runner-up, else fall back to human confirmation.
	if status == StatusAuto && cfg.Margin > 0 && secondScore > math.Inf(-1) &&
		bestScore-secondScore < cfg.Margin {
		status = StatusPending
		reason = "ambiguous: top-2 within margin"
	}

	return MatchDecision{
		ProfileID: bestCandidate.ProfileID,
		Name:      bestCandidate.Name,
		Score:     bestScore,
		Status:    status,
		Reason:    reason,
	}, nil
}

func MatchWithDuration(query EmbeddingWithDuration, candidates []Candidate) (MatchDecision, error) {
	if query.IsShort() {
		return MatchDecision{}, notoerr.New("segment_too_short", "segment duration below minimum", map[string]any{"duration": query.Duration, "minimum": MinDuration})
	}
	return Match(query.Embedding, candidates)
}

// RankCandidates scores query against every candidate and returns the decisions
// sorted by descending similarity, capped at topK (topK<=0 returns all). It is
// the basis for the UI's "who is this most likely?" top-N suggestion list —
// unlike Match, which collapses to a single best decision. Each decision keeps
// its auto/pending/new status so the caller can render confidence bands.
func RankCandidates(query Embedding, candidates []Candidate, topK int) ([]MatchDecision, error) {
	decisions, err := MatchCandidates(query, candidates)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(decisions, func(i, j int) bool {
		return decisions[i].Score > decisions[j].Score
	})
	if topK > 0 && len(decisions) > topK {
		decisions = decisions[:topK]
	}
	return decisions, nil
}

func MatchCandidates(query Embedding, candidates []Candidate) ([]MatchDecision, error) {
	if len(query) == 0 || len(candidates) == 0 {
		return nil, nil
	}
	decisions := make([]MatchDecision, 0, len(candidates))
	queryNorm := Normalize(query)
	for _, cand := range candidates {
		if len(cand.Centroid) != len(query) {
			continue
		}
		score := Dot(queryNorm, cand.Centroid)
		var status MatchStatus
		var reason string
		switch {
		case score >= AutoThreshold:
			status = StatusAuto
			reason = "auto match"
		case score >= PendingThreshold:
			status = StatusPending
			reason = "pending confirmation"
		default:
			status = StatusNew
			reason = "below threshold"
		}
		decisions = append(decisions, MatchDecision{
			ProfileID: cand.ProfileID,
			Name:      cand.Name,
			Score:     score,
			Status:    status,
			Reason:    reason,
		})
	}
	return decisions, nil
}
