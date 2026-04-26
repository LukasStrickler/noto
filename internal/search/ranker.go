package search

const (
	BM25K1 = 1.2
	BM25B   = 0.75
)

type BM25Config struct {
	K1 float64
	B  float64
}

func DefaultBM25Config() BM25Config {
	return BM25Config{
		K1: BM25K1,
		B:  BM25B,
	}
}

func SortByBM25(results []SearchResult) []SearchResult {
	sorted := make([]SearchResult, len(results))
	copy(sorted, results)

	for i := 0; i < len(sorted)-1; i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[j].BM25Score < sorted[i].BM25Score {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}

	return sorted
}

func FilterByResultType(results []SearchResult, resultType ResultType) []SearchResult {
	var filtered []SearchResult
	for _, r := range results {
		if r.ResultType == resultType {
			filtered = append(filtered, r)
		}
	}
	return filtered
}

func FilterByMeeting(results []SearchResult, meetingID string) []SearchResult {
	var filtered []SearchResult
	for _, r := range results {
		if r.MeetingID == meetingID {
			filtered = append(filtered, r)
		}
	}
	return filtered
}
