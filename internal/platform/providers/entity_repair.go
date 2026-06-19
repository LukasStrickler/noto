package providers

import (
	"strings"

	"github.com/lukasstrickler/noto/internal/core/artifacts"
	"github.com/lukasstrickler/noto/internal/core/entityrepair"
)

// RepairTranscriptEntities snaps low-confidence transcript words to known glossary
// entities (see internal/core/entityrepair) and keeps segment text consistent by
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

	touched := make(map[string]bool)
	for _, r := range reps {
		for k := 0; k < r.Length; k++ {
			idx := r.Index + k
			t.Words[idx].Text = out[idx]
			if sid := t.Words[idx].SegmentID; sid != "" {
				touched[sid] = true
			}
		}
	}
	rebuildSegmentText(t, touched)
	return reps
}

// rebuildSegmentText regenerates Text for the given segment IDs by space-joining their
// member words in transcript order. Only touched segments are rebuilt, so untouched
// segments keep their original provider formatting; a corrected segment is re-joined
// from its (now-fixed) words so its displayed text matches the word list.
func rebuildSegmentText(t *artifacts.Transcript, touched map[string]bool) {
	if len(touched) == 0 {
		return
	}
	parts := make(map[string][]string, len(touched))
	for _, w := range t.Words {
		if w.SegmentID != "" && touched[w.SegmentID] {
			parts[w.SegmentID] = append(parts[w.SegmentID], w.Text)
		}
	}
	for i := range t.Segments {
		if toks, ok := parts[t.Segments[i].ID]; ok && len(toks) > 0 {
			t.Segments[i].Text = strings.Join(toks, " ")
		}
	}
}
