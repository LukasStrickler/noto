package service

import (
	"context"
	"sort"

	"github.com/lukasstrickler/noto/internal/core/speakers"
	"github.com/lukasstrickler/noto/internal/platform/speakerstore"
	"github.com/lukasstrickler/noto/internal/transport/notoapi"
)

// Context-prior tuning. The prior only RE-RANKS the suggestion list for
// unresolved speakers; it never raises a score past the auto threshold and
// never affects the auto/silent-merge decision (that stays purely voice ≥0.70,
// decided earlier in matchSpeakers). Weights are deliberately small so voice
// similarity dominates and context only breaks near-ties — e.g. surfacing the
// recurring fourth attendee of a team that always meets together.
const (
	suggestPoolSize  = 8    // voice candidates considered before re-ranking
	suggestTopN      = 3    // suggestions surfaced to the UI
	suggestMinScore  = 0.45 // never suggest a person the voice doesn't plausibly match
	coAttendWeight   = 0.05 // boost per the co-attendance prior (max)
	coAttendSaturate = 3.0  // co-meetings at which the boost saturates
	frequencyWeight  = 0.02 // boost per the recurring-person prior (max)
	frequencySatur   = 10.0 // total meetings at which that boost saturates
)

// enrichMappings turns stored mappings into API mappings, resolving each
// linked profile's name and, for unresolved speakers (pending/new/unmatched
// with a stored voiceprint), attaching a ranked top-N of who they likely are.
func (s *Service) enrichMappings(meetingID string, mappings []speakerstore.MeetingSpeakerMapping) []notoapi.MeetingSpeakerMapping {
	profiles, err := s.speakerProfiles.List(context.Background())
	if err != nil {
		return s.mappingsToAPI(mappings)
	}
	nameByID := make(map[string]string, len(profiles))
	candidates := make([]speakers.Candidate, 0, len(profiles))
	for _, p := range profiles {
		nameByID[p.ID] = p.DisplayName
		if len(p.EmbeddingVector) > 0 {
			candidates = append(candidates, speakers.Candidate{ProfileID: p.ID, Name: p.DisplayName, Centroid: p.EmbeddingVector})
		}
	}

	// Profiles already confidently identified in THIS meeting anchor the
	// co-attendance prior.
	identified := map[string]bool{}
	for _, m := range mappings {
		if m.ProfileID != nil && (m.MatchStatus == "auto" || m.MatchStatus == "manual") {
			identified[*m.ProfileID] = true
		}
	}
	anchorMeetings := s.meetingsForProfiles(identified, meetingID)

	out := make([]notoapi.MeetingSpeakerMapping, 0, len(mappings))
	for _, m := range mappings {
		api := s.mappingToAPI(m)
		if m.ProfileID != nil {
			api.ProfileName = nameByID[*m.ProfileID]
		}
		if isUnresolved(m.MatchStatus) && len(m.EmbeddingVector) > 0 && len(candidates) > 0 {
			api.Candidates = s.rankSuggestions(m.EmbeddingVector, candidates, anchorMeetings)
		}
		out = append(out, api)
	}
	return out
}

func isUnresolved(status string) bool {
	switch status {
	case "pending", "new", "unmatched", "":
		return true
	}
	return false
}

// rankSuggestions ranks candidates by voice similarity, then nudges the order
// with the meeting-context prior, returning the top-N as API candidates.
func (s *Service) rankSuggestions(query []float64, candidates []speakers.Candidate, anchorMeetings map[string]bool) []notoapi.SpeakerCandidate {
	ranked, err := speakers.RankCandidates(query, candidates, suggestPoolSize)
	if err != nil || len(ranked) == 0 {
		return nil
	}
	type scored struct {
		dec    speakers.MatchDecision
		final  float64
		reason string
	}
	pool := make([]scored, 0, len(ranked))
	for _, d := range ranked {
		// Don't surface a person whose voice barely matches — a confusing
		// suggestion is worse than none ("unknown — name them" is clearer).
		if d.Score < suggestMinScore {
			continue
		}
		boost, reason := 0.0, ""
		if len(anchorMeetings) > 0 {
			co := s.coAttendance(d.ProfileID, anchorMeetings)
			if co > 0 {
				b := coAttendWeight * minf(1, float64(co)/coAttendSaturate)
				boost += b
				reason = "frequent co-attendee"
			}
		}
		if cnt := s.profileMeetingCount(d.ProfileID); cnt > 1 {
			boost += frequencyWeight * minf(1, float64(cnt)/frequencySatur)
		}
		pool = append(pool, scored{dec: d, final: d.Score + boost, reason: reason})
	}
	sort.SliceStable(pool, func(i, j int) bool { return pool[i].final > pool[j].final })

	n := suggestTopN
	if n > len(pool) {
		n = len(pool)
	}
	out := make([]notoapi.SpeakerCandidate, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, notoapi.SpeakerCandidate{
			ProfileID:   pool[i].dec.ProfileID,
			DisplayName: pool[i].dec.Name,
			Score:       pool[i].dec.Score, // report the voice score, not the boosted value
			Reason:      pool[i].reason,
			Rank:        i + 1,
		})
	}
	return out
}

// meetingsForProfiles returns the set of meeting IDs (excluding `exclude`) in
// which any of the given profiles appears.
func (s *Service) meetingsForProfiles(profileIDs map[string]bool, exclude string) map[string]bool {
	out := map[string]bool{}
	for pid := range profileIDs {
		maps, err := s.meetingMappings.ListByProfile(context.Background(), pid)
		if err != nil {
			continue
		}
		for _, m := range maps {
			if m.MeetingID != exclude {
				out[m.MeetingID] = true
			}
		}
	}
	return out
}

// coAttendance counts how many of the anchor meetings the candidate also
// appeared in — high when this person habitually shows up with the people
// already identified here.
func (s *Service) coAttendance(profileID string, anchorMeetings map[string]bool) int {
	maps, err := s.meetingMappings.ListByProfile(context.Background(), profileID)
	if err != nil {
		return 0
	}
	n := 0
	for _, m := range maps {
		if anchorMeetings[m.MeetingID] {
			n++
		}
	}
	return n
}

func (s *Service) profileMeetingCount(profileID string) int {
	maps, err := s.meetingMappings.ListByProfile(context.Background(), profileID)
	if err != nil {
		return 0
	}
	return len(maps)
}

func minf(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
