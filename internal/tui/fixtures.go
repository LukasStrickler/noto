package tui

import "strings"

type fixtureStore struct {
	meetings []MeetingFixture
}

func newFixtureStore() fixtureStore {
	return fixtureStore{meetings: fixtureMeetings()}
}

func (s fixtureStore) ListMeetings() []MeetingFixture {
	return append([]MeetingFixture(nil), s.meetings...)
}

func (s fixtureStore) GetMeeting(id string) (MeetingFixture, bool) {
	for _, meeting := range s.meetings {
		if meeting.ID == id {
			return meeting, true
		}
	}
	return MeetingFixture{}, false
}

func (s fixtureStore) SearchSegments(query string) []SearchResult {
	query = strings.TrimSpace(strings.ToLower(query))
	var results []SearchResult
	for _, meeting := range s.meetings {
		for _, seg := range meeting.Segments {
			haystack := strings.ToLower(strings.Join([]string{
				meeting.Title,
				meeting.Summary,
				seg.ID,
				seg.Time,
				seg.Speaker,
				seg.Role,
				seg.Text,
			}, " "))
			if query == "" || strings.Contains(haystack, query) {
				results = append(results, SearchResult{MeetingID: meeting.ID, MeetingTitle: meeting.Title, Segment: seg})
			}
		}
	}
	return results
}

func fixtureMeetings() []MeetingFixture {
	return []MeetingFixture{{
		ID:       "mtg_product_sync",
		Title:    "Product architecture sync",
		Date:     "2026-04-24",
		Duration: "42m",
		Status:   "summarized",
		Speakers: 3,
		Summary:  "Terminal-first V1 with native capture helper, JSON artifacts, provider routing, and citation-friendly transcript evidence.",
		Decisions: []string{
			"Keep Noto as a terminal-first meeting memory workbench.",
			"Route production LLM work only through OpenRouter.",
		},
		Risks: []string{
			"Provider keys or macOS capture permissions can block live jobs.",
			"Diarization quality must stay measurable across speech providers.",
		},
		Actions: []string{"Add transcript fixture", "Wire search index", "Implement capture helper"},
		Files:   []string{"transcript.diarized.json", "summary.json", "summary.md"},
		Segments: []TranscriptSegment{
			{"seg_000210", "00:14:02", "Maya", "participants", "Terminal UI should be the primary interface."},
			{"seg_000245", "00:16:44", "Lukas", "local_speaker", "Agents need JSON and direct artifact paths, not a UI they scrape."},
			{"seg_000301", "00:22:10", "Chen", "participants", "Provider benchmarks need WER, DER, latency, and cost."},
		},
	}, {
		ID:       "mtg_vendor_benchmark",
		Title:    "Vendor benchmark",
		Date:     "2026-04-23",
		Duration: "28m",
		Status:   "todo",
		Speakers: 2,
		Summary:  "Fixture meeting for provider comparison and benchmark result rendering.",
		Decisions: []string{
			"Benchmark-selected remains a routing option, not a fourth provider.",
		},
		Risks:   []string{"Cost can dominate quality gains on long recurring meetings."},
		Actions: []string{"Run AMI sample", "Compare STT providers"},
		Files:   []string{"audio.json"},
		Segments: []TranscriptSegment{
			{"seg_000120", "00:08:41", "Sam", "participants", "Cost and diarization quality both matter for the default provider."},
			{"seg_000165", "00:11:03", "Lukas", "local_speaker", "The UI should expose cost and data-leaves-device state before live jobs."},
		},
	}}
}

func fixtureJobs() []JobState {
	return []JobState{
		{Name: "capture", Status: "idle", Detail: "native helper idle"},
		{Name: "stt", Status: "idle", Detail: "waiting for audio"},
		{Name: "summary", Status: "idle", Detail: "OpenRouter ready when key exists"},
		{Name: "index", Status: "clean", Detail: "fixture index loaded"},
	}
}

func fixtureRecorder() RecorderState {
	return RecorderState{
		State:          "idle",
		Title:          "Untitled meeting",
		MicDB:          -42,
		ParticipantsDB: -48,
		Permission:     "pending native helper",
		Retention:      "delete raw audio after valid transcript",
	}
}

func fixtureStorage() StorageState {
	return StorageState{
		Schema:     "ok",
		Checksum:   "ok",
		Index:      "clean",
		Verified:   true,
		LastResult: "not run in this session",
	}
}
