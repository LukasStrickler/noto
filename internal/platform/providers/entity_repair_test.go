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

func TestRepairTranscriptEntities_MergesSplitEntityAndRebuilds(t *testing.T) {
	tr := &artifacts.Transcript{
		Segments: []artifacts.Segment{
			{ID: "s1", Text: "we use data dog", WordIDs: []string{"w1", "w2", "w3", "w4"}},
		},
		Words: []artifacts.Word{
			{ID: "w1", SegmentID: "s1", Text: "we", StartSeconds: 0, EndSeconds: 1},
			{ID: "w2", SegmentID: "s1", Text: "use", StartSeconds: 1, EndSeconds: 2},
			{ID: "w3", SegmentID: "s1", Text: "data", StartSeconds: 2, EndSeconds: 3},
			{ID: "w4", SegmentID: "s1", Text: "dog", StartSeconds: 3, EndSeconds: 4},
		},
	}
	reps := RepairTranscriptEntities(tr, []string{"Datadog"}, entityrepair.DefaultOptions())
	if len(reps) != 1 {
		t.Fatalf("expected 1 merge repair, got %+v", reps)
	}
	if len(tr.Words) != 3 {
		t.Fatalf("merged word should be dropped (4→3), got %d: %+v", len(tr.Words), tr.Words)
	}
	last := tr.Words[2]
	if last.Text != "Datadog" || last.StartSeconds != 2 || last.EndSeconds != 4 {
		t.Errorf("merged word should span the joined timestamps, got %+v", last)
	}
	if tr.Segments[0].Text != "we use Datadog" {
		t.Errorf("segment text not rebuilt: %q", tr.Segments[0].Text)
	}
	if len(tr.Segments[0].WordIDs) != 3 || tr.Segments[0].WordIDs[2] != "w3" {
		t.Errorf("segment word-ids not rebuilt to surviving words: %v", tr.Segments[0].WordIDs)
	}
}

func TestRepairTranscriptEntities_SkipsCrossSegmentMerge(t *testing.T) {
	// "data" and "dog" landed in different diarized segments — a merge here would drop
	// w2 and empty s2. The repair must be skipped, leaving everything intact.
	tr := &artifacts.Transcript{
		Segments: []artifacts.Segment{
			{ID: "s1", Text: "use data", WordIDs: []string{"w1", "w2"}},
			{ID: "s2", Text: "dog now", WordIDs: []string{"w3", "w4"}},
		},
		Words: []artifacts.Word{
			{ID: "w1", SegmentID: "s1", Text: "use", StartSeconds: 0, EndSeconds: 1},
			{ID: "w2", SegmentID: "s1", Text: "data", StartSeconds: 1, EndSeconds: 2},
			{ID: "w3", SegmentID: "s2", Text: "dog", StartSeconds: 2, EndSeconds: 3},
			{ID: "w4", SegmentID: "s2", Text: "now", StartSeconds: 3, EndSeconds: 4},
		},
	}
	reps := RepairTranscriptEntities(tr, []string{"Datadog"}, entityrepair.DefaultOptions())
	if len(reps) != 0 {
		t.Errorf("a cross-segment merge must be skipped, got %+v", reps)
	}
	if len(tr.Words) != 4 {
		t.Errorf("no word should be dropped, got %d", len(tr.Words))
	}
	if tr.Segments[0].Text != "use data" || tr.Segments[1].Text != "dog now" {
		t.Errorf("segments must be untouched: %q / %q", tr.Segments[0].Text, tr.Segments[1].Text)
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
