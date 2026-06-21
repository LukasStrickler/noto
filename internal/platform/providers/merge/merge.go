// Package merge joins a speaker-agnostic STT transcript with a diarizer's speaker
// turns into one attributed transcript — the local pipeline's "who said what" step.
//
// A local STT emits words with timestamps but no speaker; a separate Diarizer
// emits speaker turns but no text. Attribute assigns each word to the turn
// covering its midpoint, stamps the speaker onto words and re-derived segments,
// and rebuilds the Speakers list — the same shape a bundled inline-diarizing
// pipeline produces, so downstream (embedding, matching, scoring) is identical.
//
// This is the reference implementation production adopts; the benchmark e2e chain
// uses it to score compounding STT+diarization error (cpWER / SA-WER).
package merge

import (
	"fmt"
	"sort"
	"strings"

	"github.com/lukasstrickler/noto/internal/core/artifacts"
	"github.com/lukasstrickler/noto/internal/platform/providers/diarize"
)

// SegmentGapSeconds starts a new segment when the silence between same-speaker
// words exceeds it.
const SegmentGapSeconds = 0.6

// Attribute returns a new transcript with each word and segment attributed to a
// diarizer speaker. Words are assigned to the turn covering their midpoint (or,
// failing that, the turn with the most overlap, else the nearest in time). With
// no turns, speakers are left empty. The input transcript is not mutated.
func Attribute(tr *artifacts.Transcript, turns []diarize.Turn) *artifacts.Transcript {
	out := &artifacts.Transcript{
		SchemaVersion:   tr.SchemaVersion,
		MeetingID:       tr.MeetingID,
		Language:        tr.Language,
		DurationSeconds: tr.DurationSeconds,
		Provider:        tr.Provider,
		Capabilities:    tr.Capabilities,
	}

	words := append([]artifacts.Word(nil), tr.Words...)
	sort.SliceStable(words, func(i, j int) bool { return words[i].StartSeconds < words[j].StartSeconds })

	sorted := append([]diarize.Turn(nil), turns...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].StartSeconds < sorted[j].StartSeconds })

	for i := range words {
		words[i].SpeakerID = assign(words[i], sorted)
	}

	out.Words, out.Segments = resegment(words)
	out.Speakers = speakers(out.Words)
	return out
}

// assign picks the best speaker for a word: the turn containing its midpoint,
// else the turn with the greatest overlap, else the nearest turn in time.
func assign(w artifacts.Word, turns []diarize.Turn) string {
	if len(turns) == 0 {
		return ""
	}
	mid := (w.StartSeconds + w.EndSeconds) / 2

	bestOverlap, bestSpk := 0.0, ""
	nearestGap, nearestSpk := -1.0, ""
	for _, t := range turns {
		if mid >= t.StartSeconds && mid < t.EndSeconds {
			return t.Speaker // midpoint inside the turn — unambiguous
		}
		ov := overlap(w.StartSeconds, w.EndSeconds, t.StartSeconds, t.EndSeconds)
		if ov > bestOverlap {
			bestOverlap, bestSpk = ov, t.Speaker
		}
		gap := gapTo(mid, t)
		if nearestGap < 0 || gap < nearestGap {
			nearestGap, nearestSpk = gap, t.Speaker
		}
	}
	if bestOverlap > 0 {
		return bestSpk
	}
	return nearestSpk
}

func overlap(aStart, aEnd, bStart, bEnd float64) float64 {
	lo := max(aStart, bStart)
	hi := min(aEnd, bEnd)
	if hi <= lo {
		return 0
	}
	return hi - lo
}

func gapTo(t float64, turn diarize.Turn) float64 {
	if t < turn.StartSeconds {
		return turn.StartSeconds - t
	}
	if t > turn.EndSeconds {
		return t - turn.EndSeconds
	}
	return 0
}

// resegment rebuilds segments from attributed words: a new segment starts when
// the speaker changes or the pause exceeds SegmentGapSeconds. Returns the words
// (with segment IDs linked) and the segments.
func resegment(words []artifacts.Word) ([]artifacts.Word, []artifacts.Segment) {
	var segs []artifacts.Segment
	segIdx := -1
	var prevEnd float64
	prevSpk := "\x00" // sentinel: differs from any real speaker on the first word

	for i := range words {
		w := &words[i]
		newSeg := segIdx < 0 || w.SpeakerID != prevSpk || w.StartSeconds-prevEnd > SegmentGapSeconds
		if newSeg {
			segIdx++
			segs = append(segs, artifacts.Segment{
				ID:           fmt.Sprintf("s%d", segIdx),
				SpeakerID:    w.SpeakerID,
				StartSeconds: w.StartSeconds,
				EndSeconds:   w.EndSeconds,
			})
		}
		seg := &segs[segIdx]
		if seg.Text == "" {
			seg.Text = w.Text
		} else {
			seg.Text += " " + w.Text
		}
		seg.EndSeconds = w.EndSeconds
		w.SegmentID = seg.ID
		seg.WordIDs = append(seg.WordIDs, w.ID)

		prevEnd = w.EndSeconds
		prevSpk = w.SpeakerID
	}
	return words, segs
}

// speakers builds the distinct speaker list from attributed words, in first-seen
// order, skipping the empty (unattributed) label.
func speakers(words []artifacts.Word) []artifacts.Speaker {
	seen := map[string]bool{}
	var out []artifacts.Speaker
	for _, w := range words {
		if w.SpeakerID == "" || seen[w.SpeakerID] {
			continue
		}
		seen[w.SpeakerID] = true
		out = append(out, artifacts.Speaker{
			ID:            w.SpeakerID,
			Label:         w.SpeakerID,
			Origin:        "diarized",
			ProviderLabel: w.SpeakerID,
		})
	}
	return out
}

// WordsBySpeaker groups an attributed transcript's words per speaker, normalized
// into tokens — the shape metrics.CpWER / metrics.SAWER consume. Tokenization
// goes through strings.Fields after the caller has normalized; callers that need
// metrics-normalized tokens should pass through metrics.Normalize. This helper
// returns raw per-speaker text joined for the caller to normalize.
func WordsBySpeaker(tr *artifacts.Transcript) map[string]string {
	bySpk := map[string][]string{}
	words := append([]artifacts.Word(nil), tr.Words...)
	sort.SliceStable(words, func(i, j int) bool { return words[i].StartSeconds < words[j].StartSeconds })
	for _, w := range words {
		bySpk[w.SpeakerID] = append(bySpk[w.SpeakerID], w.Text)
	}
	out := make(map[string]string, len(bySpk))
	for spk, toks := range bySpk {
		out[spk] = strings.Join(toks, " ")
	}
	return out
}
