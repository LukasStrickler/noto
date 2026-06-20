package providers

import (
	"strings"

	"github.com/lukasstrickler/noto/internal/core/artifacts"
	"github.com/lukasstrickler/noto/internal/core/entityrepair"
)

// RepairTranscriptEntities snaps near-miss transcript words (close-but-not-exact to a
// known glossary entity) to that entity (see internal/core/entityrepair) and keeps
// segment text consistent by
// rebuilding each TOUCHED segment's Text from its corrected member words. It is a no-op
// when there are no glossary terms or the provider emitted no word-level output (the
// segment-only offload path), so it is safe to call unconditionally — the glossary's
// presence is the gate. Returns the repairs applied, for provenance/logging.
func RepairTranscriptEntities(t *artifacts.Transcript, terms []string, opts entityrepair.Options) []entityrepair.Repair {
	if t == nil || len(t.Words) == 0 || len(terms) == 0 {
		return nil
	}
	in := make([]entityrepair.Word, len(t.Words))
	for i, w := range t.Words {
		in[i] = entityrepair.Word{Text: w.Text, Confidence: w.Confidence}
	}
	out, reps := entityrepair.Apply(in, terms, opts)
	if len(reps) == 0 {
		return nil
	}

	// Apply the corrected texts; a "" output marks a word merged away (a split entity
	// rejoined), which we drop after extending the kept word to cover its span so the
	// timeline stays gapless. A repair that spans a segment boundary is SKIPPED — merging
	// across segments could empty one segment and desync its text from its words; an
	// entity split across two diarized segments is an edge case not worth that risk.
	drop := make([]bool, len(t.Words))
	touched := make(map[string]bool)
	applied := reps[:0:0]
	for _, r := range reps {
		if !withinOneSegment(t.Words, r.Index, r.Length) {
			continue
		}
		applied = append(applied, r)
		lastKept := r.Index
		for k := 0; k < r.Length; k++ {
			idx := r.Index + k
			if out[idx] == "" {
				drop[idx] = true
			} else {
				t.Words[idx].Text = out[idx]
				lastKept = idx
			}
			if sid := t.Words[idx].SegmentID; sid != "" {
				touched[sid] = true
			}
		}
		if end := r.Index + r.Length - 1; t.Words[end].EndSeconds > t.Words[lastKept].EndSeconds {
			t.Words[lastKept].EndSeconds = t.Words[end].EndSeconds
		}
	}
	if len(applied) == 0 {
		return nil
	}

	if anyTrue(drop) {
		kept := t.Words[:0:0]
		for i, w := range t.Words {
			if !drop[i] {
				kept = append(kept, w)
			}
		}
		t.Words = kept
	}
	rebuildTouchedSegments(t, touched)
	return applied
}

// withinOneSegment reports whether all words in [start,start+length) share one segment,
// so a repair never crosses a segment boundary.
func withinOneSegment(ws []artifacts.Word, start, length int) bool {
	if length <= 1 {
		return true
	}
	sid := ws[start].SegmentID
	for k := 1; k < length; k++ {
		if ws[start+k].SegmentID != sid {
			return false
		}
	}
	return true
}

func anyTrue(b []bool) bool {
	for _, v := range b {
		if v {
			return true
		}
	}
	return false
}

// rebuildTouchedSegments regenerates Text and WordIDs for the touched segments from their
// (surviving) member words in transcript order. Only touched segments are rebuilt, so
// untouched segments keep their original provider formatting; a corrected segment's text
// and word-id list are re-derived so they stay consistent with the merged word list.
func rebuildTouchedSegments(t *artifacts.Transcript, touched map[string]bool) {
	if len(touched) == 0 {
		return
	}
	texts := make(map[string][]string, len(touched))
	ids := make(map[string][]string, len(touched))
	for _, w := range t.Words {
		if w.SegmentID != "" && touched[w.SegmentID] {
			texts[w.SegmentID] = append(texts[w.SegmentID], w.Text)
			ids[w.SegmentID] = append(ids[w.SegmentID], w.ID)
		}
	}
	for i := range t.Segments {
		id := t.Segments[i].ID
		if toks, ok := texts[id]; ok && len(toks) > 0 {
			t.Segments[i].Text = strings.Join(toks, " ")
			t.Segments[i].WordIDs = ids[id]
		}
	}
}
