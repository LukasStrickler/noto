package providers

import (
	"testing"

	"github.com/lukasstrickler/noto/internal/core/artifacts"
	"github.com/lukasstrickler/noto/internal/core/entityrepair"
)

func TestRepairTranscriptEntities_FixesWordsAndTouchedSegment(t *testing.T) {
	tr := &artifacts.Transcript{
		Segments: []artifacts.Segment{
			{ID: "s1", Text: "deploy the kubernates cluster"},
			{ID: "s2", Text: "all good"},
		},
		Words: []artifacts.Word{
			{ID: "w1", SegmentID: "s1", Text: "deploy"},
			{ID: "w2", SegmentID: "s1", Text: "the"},
			{ID: "w3", SegmentID: "s1", Text: "kubernates"},
			{ID: "w4", SegmentID: "s1", Text: "cluster"},
			{ID: "w5", SegmentID: "s2", Text: "all"},
			{ID: "w6", SegmentID: "s2", Text: "good"},
		},
	}
	reps := RepairTranscriptEntities(tr, []string{"Kubernetes"}, entityrepair.DefaultOptions())
	if len(reps) != 1 {
		t.Fatalf("expected 1 repair, got %+v", reps)
	}
	if tr.Words[2].Text != "Kubernetes" {
		t.Errorf("word not fixed: %q", tr.Words[2].Text)
	}
	if tr.Segments[0].Text != "deploy the Kubernetes cluster" {
		t.Errorf("touched segment text not rebuilt: %q", tr.Segments[0].Text)
	}
	if tr.Segments[1].Text != "all good" {
		t.Errorf("untouched segment must keep its original text, got %q", tr.Segments[1].Text)
	}
}

func TestRepairTranscriptEntities_NoOpWithoutTermsOrWords(t *testing.T) {
	tr := &artifacts.Transcript{Words: []artifacts.Word{{Text: "kubernates"}}}
	if reps := RepairTranscriptEntities(tr, nil, entityrepair.DefaultOptions()); reps != nil {
		t.Error("no terms → no repairs")
	}
	segOnly := &artifacts.Transcript{Segments: []artifacts.Segment{{Text: "kubernates"}}}
	if reps := RepairTranscriptEntities(segOnly, []string{"Kubernetes"}, entityrepair.DefaultOptions()); reps != nil {
		t.Error("no word-level output → no repairs (segment-only offload path)")
	}
}
