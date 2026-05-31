package speakers

import (
	"math"

	"github.com/lukasstrickler/noto/internal/notoerr"
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

func Match(query Embedding, candidates []Candidate) (MatchDecision, error) {
	if len(query) == 0 {
		return MatchDecision{Status: StatusNew, Reason: "empty query embedding"}, nil
	}
	if len(candidates) == 0 {
		return MatchDecision{Status: StatusNew, Reason: "no candidates provided"}, nil
	}
	queryNorm := Normalize(query)
	var bestScore float64
	var bestCandidate *Candidate

	for i := range candidates {
		cand := &candidates[i]
		if len(cand.Centroid) != len(query) {
			return MatchDecision{}, DimError(len(query), len(cand.Centroid))
		}
		score := Dot(queryNorm, cand.Centroid)
		if score > bestScore || bestCandidate == nil {
			bestScore = score
			bestCandidate = cand
		}
	}

	if bestCandidate == nil {
		return MatchDecision{Status: StatusNew, Reason: "no matching candidate found"}, nil
	}

	var status MatchStatus
	var reason string
	switch {
	case bestScore >= AutoThreshold:
		status = StatusAuto
		reason = "auto match"
	case bestScore >= PendingThreshold:
		status = StatusPending
		reason = "pending confirmation"
	default:
		status = StatusNew
		reason = "below threshold"
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
