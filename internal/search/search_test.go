package search

import (
	"os"
	"path/filepath"
	"testing"
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
		Decisions:  []SummaryItem{},
		ActionItems: []ActionItem{},
		Risks:      []SummaryItem{},
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
		Decisions:  []SummaryItem{},
		ActionItems: []ActionItem{},
		Risks:      []SummaryItem{},
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
		Decisions:  []SummaryItem{},
		ActionItems: []ActionItem{},
		Risks:      []SummaryItem{},
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
		Title:    "Test meeting from input",
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
