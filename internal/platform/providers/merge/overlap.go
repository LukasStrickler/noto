package merge

import (
	"sort"

	"github.com/lukasstrickler/noto/internal/core/artifacts"
)

// --- Overlap refinement merge (ADR 0007) ---
//
// The single-channel pipeline emits ONE word-stream and Attribute pins each word
// to one speaker, so where two people talk at once the second speaker is garbled
// or lost. The refinement pass (ADR 0007) separates those overlap regions into
// per-speaker audio streams and transcribes each; MergeOverlapStreams is the
// consumer that splices both speakers' words back into the transcript, so overlap
// is transcribed INDIVIDUALLY rather than as a mix.
//
// Forward infra: this is the documented v2 merge for the deferred refinement pass;
// it is exercised by tests now and wired once the remote-compute separation lands
// (gated on Program B2). See GPU_REDESIGN_PROGRESS.md "Overlap separation".

// OverlapStream is one separated speaker's transcribed words within an overlap
// region: the words recovered from that speaker's isolated audio stream, already
// attributed to a diarized speaker (the stream→speaker assignment is done upstream
// by ECAPA/diar before this merge).
type OverlapStream struct {
	SpeakerID string
	Words     []artifacts.Word
}

// OverlapRegion is a time span where two speakers talk at once, carrying each
// speaker's separately-transcribed words.
type OverlapRegion struct {
	StartSeconds float64
	EndSeconds   float64
	Streams      []OverlapStream
}

// MergeOverlapStreams returns a refined transcript: within each overlap region the
// base transcript's mixed-channel words (whose midpoint falls in the region) are
// DROPPED and replaced by the separated streams' words, each attributed to its
// speaker — so both speakers are transcribed individually where they overlap.
// Words outside the regions are untouched. The base is not mutated; the result is
// re-segmented and its speaker list rebuilt, identical in shape to Attribute's
// output, so downstream (embedding, matching, scoring) is unchanged.
func MergeOverlapStreams(base *artifacts.Transcript, regions []OverlapRegion) *artifacts.Transcript {
	out := &artifacts.Transcript{
		SchemaVersion:   base.SchemaVersion,
		MeetingID:       base.MeetingID,
		Language:        base.Language,
		DurationSeconds: base.DurationSeconds,
		Provider:        base.Provider,
		Capabilities:    base.Capabilities,
	}

	// Keep base words whose midpoint is NOT inside any overlap region (the region's
	// mixed words are superseded by the separated streams).
	var words []artifacts.Word
	for _, w := range base.Words {
		if !inAnyRegion((w.StartSeconds+w.EndSeconds)/2, regions) {
			words = append(words, w)
		}
	}
	// Add the separated streams' words, attributed to their speakers.
	for _, r := range regions {
		for _, s := range r.Streams {
			for _, w := range s.Words {
				w.SpeakerID = s.SpeakerID
				words = append(words, w)
			}
		}
	}

	sort.SliceStable(words, func(i, j int) bool { return words[i].StartSeconds < words[j].StartSeconds })
	out.Words, out.Segments = resegment(words)
	out.Speakers = speakers(out.Words)
	return out
}

func inAnyRegion(t float64, regions []OverlapRegion) bool {
	for _, r := range regions {
		if t >= r.StartSeconds && t < r.EndSeconds {
			return true
		}
	}
	return false
}
