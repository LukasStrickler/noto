package service

import (
	"context"
	"strings"

	"github.com/lukasstrickler/noto/internal/platform/search"
	"github.com/lukasstrickler/noto/internal/transport/notoapi"
)

// Search runs a full-text search against the local FTS5 index. Empty
// queries return an empty result — callers should display the search
// UI's empty state, not an error. Punctuation-only queries (e.g. "/"
// or "?") return empty results rather than an FTS5 syntax error.
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

	// Group + rank per meeting so the UI can render
	// "5 in transcript / 2 in summary" badges and prioritise title matches.
	// Reuse the results already fetched above rather than querying FTS again.
	groups := s.search.GroupMeetings(results, 0)
	out.Meetings = make([]notoapi.MeetingHits, 0, len(groups))
	for _, g := range groups {
		mh := notoapi.MeetingHits{
			MeetingID:       g.MeetingID,
			MeetingTitle:    g.MeetingTitle,
			CreatedAt:       g.CreatedAt,
			TitleMatch:      g.TitleMatch,
			SummaryMatch:    g.SummaryMatch,
			TranscriptCount: g.TranscriptCount,
			SummaryCount:    g.SummaryCount,
			QuestionCount:   g.QuestionCount,
			Score:           g.BestScore,
			Snippet:         g.Snippet,
		}
		for _, h := range g.Hits {
			mh.TopHits = append(mh.TopHits, hitFromSearchResult(h))
		}
		out.Meetings = append(out.Meetings, mh)
	}
	return out, nil
}

func hitFromSearchResult(r search.SearchResult) notoapi.SearchHit {
	return notoapi.SearchHit{
		MeetingID:    r.MeetingID,
		MeetingTitle: r.Title,
		SegmentID:    r.SegmentID,
		Speaker:      r.Speaker,
		Timestamp:    r.Timestamp,
		Snippet:      r.Snippet,
		BM25Score:    r.BM25Score,
		ResultType:   string(r.ResultType),
	}
}
