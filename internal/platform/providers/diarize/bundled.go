package diarize

import (
	"context"
	"sort"

	"github.com/lukasstrickler/noto/internal/core/artifacts"
	"github.com/lukasstrickler/noto/internal/platform/providers/stt"
)

// BundledDiarizer adapts an stt.STTProvider that diarizes inline (one that
// returns speaker-labelled segments) into the standalone Diarizer seam, by
// projecting the transcript's segments to turns.
//
// It runs the full STT transcribe and discards the words — wasteful, but it
// makes the *current* production diarization scoreable through the same DER path
// as a local diarizer, giving the diarize benchmark a reference column on day
// one. A local diarizer is a drop-in replacement on the Diarizer interface.
type BundledDiarizer struct {
	STT stt.STTProvider
}

// NewBundledDiarizer wraps an STTProvider as a Diarizer.
func NewBundledDiarizer(s stt.STTProvider) *BundledDiarizer { return &BundledDiarizer{STT: s} }

// ProviderID tags turns as coming from the wrapped provider's bundled diarizer.
func (b *BundledDiarizer) ProviderID() string { return b.STT.ProviderID() + "+bundled-diar" }

// Diarize transcribes with the wrapped provider and projects the resulting
// speaker-labelled segments to turns.
func (b *BundledDiarizer) Diarize(ctx context.Context, audio []byte, opts DiarizeOptions) ([]Turn, error) {
	tr, err := b.STT.Transcribe(ctx, audio, stt.TranscribeOptions{
		NumSpeakers: opts.NumSpeakers,
		MeetingID:   opts.MeetingID,
	})
	if err != nil {
		return nil, err
	}
	return SegmentsToTurns(tr.Segments), nil
}

// SegmentsToTurns projects transcript segments to time-sorted turns: it drops
// segments with no speaker label, sorts by start time, and merges consecutive
// segments of the same speaker that touch or overlap into one turn (so a speaker
// stretch chopped into sentence segments reads as a single turn, the way a real
// diarizer emits it). It is pure — the unit of the bundled projection and a
// handy helper for the e2e merge step.
func SegmentsToTurns(segs []artifacts.Segment) []Turn {
	var turns []Turn
	for _, s := range segs {
		if s.SpeakerID == "" || s.EndSeconds <= s.StartSeconds {
			continue
		}
		turns = append(turns, Turn{Speaker: s.SpeakerID, StartSeconds: s.StartSeconds, EndSeconds: s.EndSeconds})
	}
	sort.SliceStable(turns, func(i, j int) bool {
		if turns[i].StartSeconds != turns[j].StartSeconds {
			return turns[i].StartSeconds < turns[j].StartSeconds
		}
		return turns[i].EndSeconds < turns[j].EndSeconds
	})
	return mergeAdjacent(turns)
}

// mergeAdjacent collapses runs of the same speaker whose segments touch or
// overlap (next.start <= cur.end) into a single turn. Input must be time-sorted.
func mergeAdjacent(turns []Turn) []Turn {
	if len(turns) == 0 {
		return nil
	}
	out := []Turn{turns[0]}
	for _, t := range turns[1:] {
		last := &out[len(out)-1]
		if t.Speaker == last.Speaker && t.StartSeconds <= last.EndSeconds {
			if t.EndSeconds > last.EndSeconds {
				last.EndSeconds = t.EndSeconds
			}
			continue
		}
		out = append(out, t)
	}
	return out
}
