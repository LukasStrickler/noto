package search

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNewSearchIndex(t *testing.T) {
	tmpDir := t.TempDir()
	indexPath := filepath.Join(tmpDir, "test_index.db")

	index, err := NewSearchIndex(indexPath)
	if err != nil {
		t.Fatalf("NewSearchIndex failed: %v", err)
	}
	defer index.Close()

	if index == nil {
		t.Fatal("NewSearchIndex returned nil index")
	}

	if _, err := os.Stat(indexPath); os.IsNotExist(err) {
		t.Error("index file was not created")
	}
}

func TestIndexAndSearch(t *testing.T) {
	tmpDir := t.TempDir()
	indexPath := filepath.Join(tmpDir, "test_index.db")

	index, err := NewSearchIndex(indexPath)
	if err != nil {
		t.Fatalf("NewSearchIndex failed: %v", err)
	}
	defer index.Close()

	meeting := &Meeting{
		MeetingID: "meeting-001",
		Title:     "Product roadmap discussion",
		TranscriptSegments: []TranscriptSegment{
			{
				SegmentID: "seg_001",
				Speaker:   "Speaker 1",
				Text:      "We should ship v1 by end of quarter",
				Timestamp: 0.0,
			},
			{
				SegmentID: "seg_002",
				Speaker:   "Speaker 2",
				Text:      "I agree, let's focus on the API pricing decision",
				Timestamp: 10.0,
			},
		},
		Decisions: []SummaryItem{
			{
				Text:       "Ship v1 by end of quarter",
				SpeakerIDs: []string{"speaker_1"},
			},
		},
		ActionItems: []ActionItem{
			{
				Text:  "Run benchmark suite",
				Owner: "@john",
			},
		},
		Risks: []SummaryItem{
			{
				Text: "Local transcription may exceed latency target",
			},
		},
	}

	if err := index.IndexMeeting(meeting); err != nil {
		t.Fatalf("IndexMeeting failed: %v", err)
	}

	results, err := index.Search("ship")
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	if len(results) == 0 {
		t.Fatal("Search returned no results for 'ship'")
	}

	found := false
	for _, r := range results {
		if r.MeetingID == "meeting-001" {
			found = true
			break
		}
	}
	if !found {
		t.Error("Search did not return the indexed meeting")
	}
}

func TestSearchReturnsRankedResults(t *testing.T) {
	tmpDir := t.TempDir()
	indexPath := filepath.Join(tmpDir, "test_index.db")

	index, err := NewSearchIndex(indexPath)
	if err != nil {
		t.Fatalf("NewSearchIndex failed: %v", err)
	}
	defer index.Close()

	meeting1 := &Meeting{
		MeetingID: "meeting-ship",
		Title:     "Ship v1 planning",
		TranscriptSegments: []TranscriptSegment{
			{
				SegmentID: "seg_001",
				Speaker:   "Speaker 1",
				Text:      "We will ship the product",
				Timestamp: 0.0,
			},
		},
		Decisions:   []SummaryItem{},
		ActionItems: []ActionItem{},
		Risks:       []SummaryItem{},
	}

	meeting2 := &Meeting{
		MeetingID: "meeting-ship-v2",
		Title:     "Ship v2 planning",
		TranscriptSegments: []TranscriptSegment{
			{
				SegmentID: "seg_001",
				Speaker:   "Speaker 1",
				Text:      "We will ship the product next year",
				Timestamp: 0.0,
			},
		},
		Decisions:   []SummaryItem{},
		ActionItems: []ActionItem{},
		Risks:       []SummaryItem{},
	}

	if err := index.IndexMeeting(meeting1); err != nil {
		t.Fatalf("IndexMeeting for meeting1 failed: %v", err)
	}
	if err := index.IndexMeeting(meeting2); err != nil {
		t.Fatalf("IndexMeeting for meeting2 failed: %v", err)
	}

	results, err := index.Search("ship")
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	if len(results) < 2 {
		t.Fatalf("Expected at least 2 results, got %d", len(results))
	}

	if results[0].BM25Score > results[1].BM25Score {
		t.Error("Results are not properly ranked by BM25 score")
	}
}

func TestDeleteFromIndex(t *testing.T) {
	tmpDir := t.TempDir()
	indexPath := filepath.Join(tmpDir, "test_index.db")

	index, err := NewSearchIndex(indexPath)
	if err != nil {
		t.Fatalf("NewSearchIndex failed: %v", err)
	}
	defer index.Close()

	meeting := &Meeting{
		MeetingID: "meeting-to-delete",
		Title:     "This meeting will be deleted",
		TranscriptSegments: []TranscriptSegment{
			{
				SegmentID: "seg_001",
				Speaker:   "Speaker 1",
				Text:      "This is temporary content",
				Timestamp: 0.0,
			},
		},
		Decisions:   []SummaryItem{},
		ActionItems: []ActionItem{},
		Risks:       []SummaryItem{},
	}

	if err := index.IndexMeeting(meeting); err != nil {
		t.Fatalf("IndexMeeting failed: %v", err)
	}

	results, err := index.Search("temporary")
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("Indexed meeting not found before delete")
	}

	if err := index.DeleteFromIndex("meeting-to-delete"); err != nil {
		t.Fatalf("DeleteFromIndex failed: %v", err)
	}

	results, err = index.Search("temporary")
	if err != nil {
		t.Fatalf("Search after delete failed: %v", err)
	}
	if len(results) != 0 {
		t.Error("Deleted meeting still appears in search results")
	}
}

func TestEmptyQueryReturnsError(t *testing.T) {
	tmpDir := t.TempDir()
	indexPath := filepath.Join(tmpDir, "test_index.db")

	index, err := NewSearchIndex(indexPath)
	if err != nil {
		t.Fatalf("NewSearchIndex failed: %v", err)
	}
	defer index.Close()

	_, err = index.Search("")
	if err == nil {
		t.Error("Empty query should return error")
	}
}

func TestPunctuationOnlyQueryReturnsNoResults(t *testing.T) {
	tmpDir := t.TempDir()
	indexPath := filepath.Join(tmpDir, "test_index.db")

	index, err := NewSearchIndex(indexPath)
	if err != nil {
		t.Fatalf("NewSearchIndex failed: %v", err)
	}
	defer index.Close()

	for _, q := range []string{"/", "?", ".", "(", ")", "!", "+", "*", ":"} {
		hits, err := index.Search(q)
		if err != nil {
			t.Fatalf("Search(%q) should not error: %v", q, err)
		}
		if len(hits) != 0 {
			t.Errorf("Search(%q) should return 0 hits on empty index, got %d", q, len(hits))
		}
	}
}

func TestTitleMatchOutranksBodyMatch(t *testing.T) {
	tmpDir := t.TempDir()
	indexPath := filepath.Join(tmpDir, "test_index.db")

	index, err := NewSearchIndex(indexPath)
	if err != nil {
		t.Fatalf("NewSearchIndex failed: %v", err)
	}
	defer index.Close()

	bodyOnly := &Meeting{
		MeetingID: "m-body",
		Title:     "Generic standup",
		TranscriptSegments: []TranscriptSegment{
			{SegmentID: "s1", Speaker: "A", Text: "we talked about onboarding flows today", Timestamp: 0},
		},
	}
	titleAndBody := &Meeting{
		MeetingID: "m-title",
		Title:     "Onboarding redesign",
		TranscriptSegments: []TranscriptSegment{
			{SegmentID: "s1", Speaker: "A", Text: "agenda was the onboarding redesign", Timestamp: 0},
		},
	}
	if err := index.IndexMeeting(bodyOnly); err != nil {
		t.Fatalf("IndexMeeting body: %v", err)
	}
	if err := index.IndexMeeting(titleAndBody); err != nil {
		t.Fatalf("IndexMeeting title: %v", err)
	}

	groups, err := index.SearchMeetings("onboarding", 0)
	if err != nil {
		t.Fatalf("SearchMeetings: %v", err)
	}
	if len(groups) < 2 {
		t.Fatalf("expected 2 meeting groups, got %d", len(groups))
	}
	if groups[0].MeetingID != "m-title" {
		t.Errorf("expected title-match meeting first, got %q (TitleMatch=%v) before %q (TitleMatch=%v)",
			groups[0].MeetingID, groups[0].TitleMatch, groups[1].MeetingID, groups[1].TitleMatch)
	}
}

func TestRecencyTiebreaker(t *testing.T) {
	tmpDir := t.TempDir()
	indexPath := filepath.Join(tmpDir, "test_index.db")

	index, err := NewSearchIndex(indexPath)
	if err != nil {
		t.Fatalf("NewSearchIndex failed: %v", err)
	}
	defer index.Close()

	older := &Meeting{
		MeetingID: "m-older",
		Title:     "Quarterly review",
		CreatedAt: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	newer := &Meeting{
		MeetingID: "m-newer",
		Title:     "Quarterly review",
		CreatedAt: time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC),
	}
	if err := index.IndexMeeting(older); err != nil {
		t.Fatalf("IndexMeeting older: %v", err)
	}
	if err := index.IndexMeeting(newer); err != nil {
		t.Fatalf("IndexMeeting newer: %v", err)
	}

	groups, err := index.SearchMeetings("quarterly", 0)
	if err != nil {
		t.Fatalf("SearchMeetings: %v", err)
	}
	if len(groups) < 2 {
		t.Fatalf("expected 2 groups, got %d", len(groups))
	}
	if groups[0].MeetingID != "m-newer" {
		t.Errorf("expected newer meeting first by recency tiebreaker; order was %q, %q",
			groups[0].MeetingID, groups[1].MeetingID)
	}
}

func TestMeetingHitsCounts(t *testing.T) {
	tmpDir := t.TempDir()
	indexPath := filepath.Join(tmpDir, "test_index.db")

	index, err := NewSearchIndex(indexPath)
	if err != nil {
		t.Fatalf("NewSearchIndex failed: %v", err)
	}
	defer index.Close()

	m := &Meeting{
		MeetingID: "m1",
		Title:     "Search hardening",
		TranscriptSegments: []TranscriptSegment{
			{SegmentID: "s1", Speaker: "A", Text: "search v2 ship plan"},
			{SegmentID: "s2", Speaker: "B", Text: "search index migration"},
		},
		Decisions: []SummaryItem{{Text: "ship search v2"}},
		Risks:     []SummaryItem{{Text: "search migration needs a window"}},
	}
	if err := index.IndexMeeting(m); err != nil {
		t.Fatalf("IndexMeeting: %v", err)
	}
	groups, err := index.SearchMeetings("search", 0)
	if err != nil {
		t.Fatalf("SearchMeetings: %v", err)
	}
	if len(groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(groups))
	}
	g := groups[0]
	if !g.TitleMatch {
		t.Errorf("expected TitleMatch=true")
	}
	if g.TranscriptCount != 2 {
		t.Errorf("expected 2 transcript matches, got %d", g.TranscriptCount)
	}
	// 1 decision + 1 risk both mention "search"
	if g.SummaryCount != 2 {
		t.Errorf("expected 2 summary matches (decision+risk), got %d", g.SummaryCount)
	}
}

func TestIndexMeetingFromInput(t *testing.T) {
	tmpDir := t.TempDir()
	indexPath := filepath.Join(tmpDir, "test_index.db")

	index, err := NewSearchIndex(indexPath)
	if err != nil {
		t.Fatalf("NewSearchIndex failed: %v", err)
	}
	defer index.Close()

	input := &IndexMeetingInput{
		MeetingID: "meeting-input-test",
		Title:     "Test meeting from input",
		TranscriptSegments: []TranscriptSegment{
			{
				SegmentID: "seg_001",
				Speaker:   "Speaker 1",
				Text:      "Testing the input struct",
				Timestamp: 0.0,
			},
		},
		Decisions: []SummaryItem{
			{
				Text: "Decision made from input",
			},
		},
		ActionItems: []ActionItem{
			{
				Text:  "Action from input",
				Owner: "@alice",
			},
		},
		Risks: []SummaryItem{
			{
				Text: "Risk from input",
			},
		},
	}

	if err := index.IndexMeetingFromInput(input); err != nil {
		t.Fatalf("IndexMeetingFromInput failed: %v", err)
	}

	results, err := index.Search("input")
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	if len(results) == 0 {
		t.Fatal("Indexed content not found")
	}
}

func TestSearchWithNoIndex(t *testing.T) {
	tmpDir := t.TempDir()
	indexPath := filepath.Join(tmpDir, "test_index.db")

	index, err := NewSearchIndex(indexPath)
	if err != nil {
		t.Fatalf("NewSearchIndex failed: %v", err)
	}
	defer index.Close()

	results, err := index.Search("anything")
	if err != nil {
		t.Fatalf("Search on empty index failed: %v", err)
	}

	if len(results) != 0 {
		t.Error("Empty index should return no results")
	}
}

func TestClose(t *testing.T) {
	tmpDir := t.TempDir()
	indexPath := filepath.Join(tmpDir, "test_index.db")

	index, err := NewSearchIndex(indexPath)
	if err != nil {
		t.Fatalf("NewSearchIndex failed: %v", err)
	}

	if err := index.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	if err := index.Close(); err != nil {
		t.Fatalf("Second Close should not fail: %v", err)
	}
}

func TestPrefixMatchFindsLongerToken(t *testing.T) {
	tmpDir := t.TempDir()
	index, err := NewSearchIndex(filepath.Join(tmpDir, "test_index.db"))
	if err != nil {
		t.Fatalf("NewSearchIndex failed: %v", err)
	}
	defer index.Close()

	m := &Meeting{
		MeetingID: "m-mobile",
		Title:     "Mobile beta kickoff",
		TranscriptSegments: []TranscriptSegment{
			{SegmentID: "s1", Speaker: "A", Text: "we shipped the mobile rollout", Timestamp: 0},
		},
	}
	if err := index.IndexMeeting(m); err != nil {
		t.Fatalf("IndexMeeting: %v", err)
	}

	// `mob` should prefix-match `mobile`.
	hits, err := index.Search("mob")
	if err != nil {
		t.Fatalf("Search('mob') failed: %v", err)
	}
	if len(hits) == 0 {
		t.Fatalf("expected prefix `mob` to match `mobile`, got 0 hits")
	}
}

func TestExactRanksAbovePrefixOnly(t *testing.T) {
	tmpDir := t.TempDir()
	index, err := NewSearchIndex(filepath.Join(tmpDir, "test_index.db"))
	if err != nil {
		t.Fatalf("NewSearchIndex failed: %v", err)
	}
	defer index.Close()

	exact := &Meeting{
		MeetingID: "m-exact",
		Title:     "Generic standup",
		TranscriptSegments: []TranscriptSegment{
			{SegmentID: "s1", Speaker: "A", Text: "we hit mob today", Timestamp: 0},
		},
	}
	prefixOnly := &Meeting{
		MeetingID: "m-prefix",
		Title:     "Generic standup",
		TranscriptSegments: []TranscriptSegment{
			{SegmentID: "s1", Speaker: "A", Text: "we shipped a mobile rollout", Timestamp: 0},
		},
	}
	if err := index.IndexMeeting(exact); err != nil {
		t.Fatalf("IndexMeeting exact: %v", err)
	}
	if err := index.IndexMeeting(prefixOnly); err != nil {
		t.Fatalf("IndexMeeting prefix: %v", err)
	}

	groups, err := index.SearchMeetings("mob", 0)
	if err != nil {
		t.Fatalf("SearchMeetings: %v", err)
	}
	if len(groups) < 2 {
		t.Fatalf("expected 2 hits (exact + prefix), got %d", len(groups))
	}
	if groups[0].MeetingID != "m-exact" {
		t.Errorf("expected exact-token meeting first; got %q before %q", groups[0].MeetingID, groups[1].MeetingID)
	}
}

func TestSummaryBodyMatchOutranksTranscriptOnly(t *testing.T) {
	tmpDir := t.TempDir()
	index, err := NewSearchIndex(filepath.Join(tmpDir, "test_index.db"))
	if err != nil {
		t.Fatalf("NewSearchIndex failed: %v", err)
	}
	defer index.Close()

	transcriptOnly := &Meeting{
		MeetingID: "m-transcript",
		Title:     "Standup A",
		TranscriptSegments: []TranscriptSegment{
			{SegmentID: "s1", Speaker: "A", Text: "we should talk about kubernetes routing", Timestamp: 0},
		},
	}
	summaryAndTranscript := &Meeting{
		MeetingID:    "m-summary",
		Title:        "Standup B",
		ShortSummary: "We finalized kubernetes routing and signed off on staging.",
		TranscriptSegments: []TranscriptSegment{
			{SegmentID: "s1", Speaker: "A", Text: "we discussed kubernetes routing options briefly", Timestamp: 0},
		},
	}
	if err := index.IndexMeeting(transcriptOnly); err != nil {
		t.Fatalf("IndexMeeting transcriptOnly: %v", err)
	}
	if err := index.IndexMeeting(summaryAndTranscript); err != nil {
		t.Fatalf("IndexMeeting summaryAndTranscript: %v", err)
	}

	groups, err := index.SearchMeetings("kubernetes", 0)
	if err != nil {
		t.Fatalf("SearchMeetings: %v", err)
	}
	if len(groups) < 2 {
		t.Fatalf("expected 2 meeting groups, got %d", len(groups))
	}
	if groups[0].MeetingID != "m-summary" {
		t.Errorf("expected summary-match meeting first; got %q (SummaryMatch=%v)", groups[0].MeetingID, groups[0].SummaryMatch)
	}
	if !groups[0].SummaryMatch {
		t.Errorf("expected SummaryMatch=true on top result")
	}
}

func TestQuestionsIndexedAndCounted(t *testing.T) {
	tmpDir := t.TempDir()
	index, err := NewSearchIndex(filepath.Join(tmpDir, "test_index.db"))
	if err != nil {
		t.Fatalf("NewSearchIndex failed: %v", err)
	}
	defer index.Close()

	m := &Meeting{
		MeetingID: "m-q",
		Title:     "Roadmap",
		OpenQuestions: []SummaryItem{
			{Text: "Should we hire a contractor for the mobile rewrite?"},
		},
	}
	if err := index.IndexMeeting(m); err != nil {
		t.Fatalf("IndexMeeting: %v", err)
	}

	groups, err := index.SearchMeetings("contractor", 0)
	if err != nil {
		t.Fatalf("SearchMeetings: %v", err)
	}
	if len(groups) != 1 {
		t.Fatalf("expected 1 hit, got %d", len(groups))
	}
	if groups[0].QuestionCount != 1 {
		t.Errorf("expected QuestionCount=1, got %d", groups[0].QuestionCount)
	}
}

func TestFTS5KeywordQueriesDoNotError(t *testing.T) {
	tmpDir := t.TempDir()
	index, err := NewSearchIndex(filepath.Join(tmpDir, "test_index.db"))
	if err != nil {
		t.Fatalf("NewSearchIndex failed: %v", err)
	}
	defer index.Close()

	m := &Meeting{
		MeetingID: "m-kw",
		Title:     "AND OR NOT NEAR discussion",
		TranscriptSegments: []TranscriptSegment{
			{SegmentID: "s1", Speaker: "A", Text: "we debated AND versus OR in the query", Timestamp: 0},
		},
	}
	if err := index.IndexMeeting(m); err != nil {
		t.Fatalf("IndexMeeting: %v", err)
	}

	// FTS5 reserves UPPERCASE AND/OR/NOT/NEAR as query operators. A user typing
	// one of those words must not crash search with a syntax error.
	for _, q := range []string{"AND", "OR", "NOT", "NEAR", "AND OR", "NEAR meeting"} {
		if _, err := index.Search(q); err != nil {
			t.Errorf("Search(%q) must not error on an FTS5 keyword: %v", q, err)
		}
	}
}

// TestMultiWordSearch is the regression for the bigger bug the keyword probe
// uncovered: FTS5 rejects two parenthesized groups joined by implicit AND
// (`(a) (b)`), so the space-join made EVERY multi-word query a syntax error.
// Multi-word search must work and require all words (AND semantics).
func TestMultiWordSearch(t *testing.T) {
	tmpDir := t.TempDir()
	index, err := NewSearchIndex(filepath.Join(tmpDir, "test_index.db"))
	if err != nil {
		t.Fatalf("NewSearchIndex failed: %v", err)
	}
	defer index.Close()

	both := &Meeting{
		MeetingID:          "m-both",
		Title:              "Ship the plan",
		TranscriptSegments: []TranscriptSegment{{SegmentID: "s1", Text: "we will ship the plan this quarter"}},
	}
	onlyOne := &Meeting{
		MeetingID:          "m-one",
		Title:              "Ship it",
		TranscriptSegments: []TranscriptSegment{{SegmentID: "s1", Text: "we will ship it"}},
	}
	if err := index.IndexMeeting(both); err != nil {
		t.Fatalf("IndexMeeting both: %v", err)
	}
	if err := index.IndexMeeting(onlyOne); err != nil {
		t.Fatalf("IndexMeeting onlyOne: %v", err)
	}

	results, err := index.Search("ship plan")
	if err != nil {
		t.Fatalf("multi-word Search must not error: %v", err)
	}
	// AND semantics: only the meeting containing BOTH words matches.
	if len(results) == 0 {
		t.Fatal(`Search("ship plan") returned nothing; multi-word search is broken`)
	}
	for _, r := range results {
		if r.MeetingID == "m-one" {
			t.Errorf(`Search("ship plan") matched a meeting missing "plan" — AND semantics lost`)
		}
	}
}

func TestPunctuationStillSanitized(t *testing.T) {
	tmpDir := t.TempDir()
	index, err := NewSearchIndex(filepath.Join(tmpDir, "test_index.db"))
	if err != nil {
		t.Fatalf("NewSearchIndex failed: %v", err)
	}
	defer index.Close()

	// Punctuation-only queries must remain harmless.
	for _, q := range []string{"/", "?", "(", ")", "*", ":"} {
		if _, err := index.Search(q); err != nil {
			t.Errorf("Search(%q) errored: %v", q, err)
		}
	}
}
