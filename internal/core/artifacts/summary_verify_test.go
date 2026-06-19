package artifacts

import "testing"

func TestVerifyAndScore_GroundsRealQuotesAndFlagsHallucinations(t *testing.T) {
	tr := Transcript{
		MeetingID: "m1",
		Segments: []Segment{
			{ID: "seg_000001", Text: "We will ship v1 next week."},
			{ID: "seg_000002", Text: "Let's defer the redesign for now."},
		},
	}
	s := &Summary{
		MeetingID: "m1",
		Decisions: []SummaryItem{
			// Grounded: quote (modulo punctuation/case) appears in the segment.
			{Text: "Ship v1", Evidence: []Evidence{{SegmentID: "seg_000001", Quote: "ship v1"}}},
			// Hallucinated: quote is nowhere in the cited segment.
			{Text: "Hire three engineers", Evidence: []Evidence{{SegmentID: "seg_000001", Quote: "hire three engineers"}}},
		},
		Risks: []SummaryItem{
			// No evidence at all -> ungrounded.
			{Text: "Schedule risk"},
		},
	}

	VerifyAndScore(s, tr)

	if s.Decisions[0].Confidence == 0 {
		t.Errorf("grounded decision should have confidence > 0, got %v", s.Decisions[0].Confidence)
	}
	if s.Decisions[1].Confidence != 0 {
		t.Errorf("hallucinated quote should score 0, got %v", s.Decisions[1].Confidence)
	}
	if s.Risks[0].Confidence != 0 {
		t.Errorf("evidence-less item should score 0, got %v", s.Risks[0].Confidence)
	}
	if s.Coverage == nil {
		t.Fatal("coverage not set")
	}
	if s.Coverage.ItemsTotal != 3 || s.Coverage.ItemsGrounded != 1 {
		t.Errorf("coverage counts = %+v, want total 3 / grounded 1", s.Coverage)
	}
	if got := s.Coverage.GroundingScore; got < 0.33 || got > 0.34 {
		t.Errorf("grounding score = %v, want ~0.33", got)
	}
}

func TestVerifyAndScore_WeightsBySegmentConfidence(t *testing.T) {
	low := 0.4
	tr := Transcript{
		MeetingID: "m1",
		Segments:  []Segment{{ID: "seg_000001", Text: "We will ship v1 next week.", Confidence: &low}},
	}
	s := &Summary{
		MeetingID: "m1",
		Decisions: []SummaryItem{{Text: "Ship v1", Evidence: []Evidence{{SegmentID: "seg_000001", Quote: "ship v1"}}}},
	}
	VerifyAndScore(s, tr)
	// Grounded but on a low-confidence segment -> confidence reflects it.
	if got := s.Decisions[0].Confidence; got <= 0 || got > 0.5 {
		t.Errorf("confidence = %v, want it scaled down by the 0.4 segment confidence", got)
	}
}
