package service

import (
	"context"
	"strings"

	"github.com/lukasstrickler/noto/internal/notoapi"
)

// Search runs a full-text search against the local FTS5 index.
// Empty queries return an empty result — callers should display the
// search UI's empty state, not an error.
func (s *Service) Search(_ context.Context, opts notoapi.SearchOpts) (notoapi.SearchResult, error) {
	q := strings.TrimSpace(opts.Query)
	if q == "" {
		return notoapi.SearchResult{}, nil
	}
	if s.search == nil {
		return notoapi.SearchResult{}, notoapi.NewError(notoapi.CodeInternal, "search index is unavailable", nil)
	}
	results, err := s.search.Search(q)
	if err != nil {
		return notoapi.SearchResult{}, notoapi.NewError(notoapi.CodeInternal, err.Error(), nil)
	}
	out := notoapi.SearchResult{Hits: make([]notoapi.SearchHit, 0, len(results)), Total: len(results)}
	limit := opts.Limit
	if limit <= 0 || limit > len(results) {
		limit = len(results)
	}
	for i := 0; i < limit; i++ {
		r := results[i]
		out.Hits = append(out.Hits, notoapi.SearchHit{
			MeetingID:    r.MeetingID,
			MeetingTitle: r.Title,
			SegmentID:    r.SegmentID,
			Speaker:      r.Speaker,
			Timestamp:    r.Timestamp,
			Snippet:      r.Snippet,
			BM25Score:    r.BM25Score,
			ResultType:   string(r.ResultType),
		})
	}
	return out, nil
}
