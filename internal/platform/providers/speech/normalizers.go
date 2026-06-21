package speech

import (
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/lukasstrickler/noto/internal/core/artifacts"
)

// TranscriptNormalizer is the interface for transcript normalizers.
// Each normalizer transforms a transcript in a specific way.
type TranscriptNormalizer interface {
	Normalize(transcript *artifacts.Transcript) (*artifacts.Transcript, error)
}

// NopNormalizer is a no-op normalizer that returns the transcript unchanged.
type NopNormalizer struct{}

// Normalize returns the transcript unchanged.
func (n *NopNormalizer) Normalize(transcript *artifacts.Transcript) (*artifacts.Transcript, error) {
	return transcript, nil
}

// DiarizationNormalizer merges adjacent segments from the same speaker
// when they are within the configured gap threshold (default 200ms).
type DiarizationNormalizer struct {
	// GapThresholdSeconds is the maximum gap between segments to merge.
	// Defaults to 0.2 seconds (200ms).
	GapThresholdSeconds float64
}

// NewDiarizationNormalizer creates a DiarizationNormalizer with default settings.
func NewDiarizationNormalizer() *DiarizationNormalizer {
	return &DiarizationNormalizer{
		GapThresholdSeconds: 0.2,
	}
}

// Normalize merges adjacent same-speaker segments within the gap threshold.
// Speaker changes are respected — we only merge segments from the same speaker.
func (n *DiarizationNormalizer) Normalize(transcript *artifacts.Transcript) (*artifacts.Transcript, error) {
	if transcript == nil {
		return nil, nil
	}
	if len(transcript.Segments) == 0 {
		return copyTranscript(transcript), nil
	}

	gap := n.GapThresholdSeconds
	if gap == 0 {
		gap = 0.2
	}

	// Build new segments by merging
	var merged []artifacts.Segment
	var current *artifacts.Segment

	for _, seg := range transcript.Segments {
		if current == nil {
			first := copySegment(seg)
			current = &first
			continue
		}

		// Check if we can merge: same speaker and within gap threshold
		if current.SpeakerID == seg.SpeakerID {
			gapBetween := seg.StartSeconds - current.EndSeconds
			if gapBetween >= 0 && gapBetween <= gap {
				currentDuration := current.EndSeconds - current.StartSeconds
				segDuration := seg.EndSeconds - seg.StartSeconds
				current.EndSeconds = seg.EndSeconds
				current.Text = current.Text + " " + seg.Text
				current.WordIDs = append(current.WordIDs, seg.WordIDs...)
				if current.Confidence != nil && seg.Confidence != nil {
					totalDuration := currentDuration + segDuration
					if totalDuration > 0 {
						weightedAvg := (*current.Confidence*currentDuration + *seg.Confidence*segDuration) / totalDuration
						current.Confidence = &weightedAvg
					}
				} else if seg.Confidence != nil {
					current.Confidence = seg.Confidence
				}
				continue
			}
		}

		// Cannot merge: push current and start new
		merged = append(merged, *current)
		newSeg := copySegment(seg)
		current = &newSeg
	}

	if current != nil {
		merged = append(merged, *current)
	}

	result := copyTranscript(transcript)
	result.Segments = merged
	return result, nil
}

// TimestampNormalizer fixes overlapping timestamps and flags gaps > 30 seconds.
type TimestampNormalizer struct {
	// GapThresholdSeconds is the minimum gap to flag as a potential gap.
	// Defaults to 30 seconds.
	GapThresholdSeconds float64
}

// NewTimestampNormalizer creates a TimestampNormalizer with default settings.
func NewTimestampNormalizer() *TimestampNormalizer {
	return &TimestampNormalizer{
		GapThresholdSeconds: 30.0,
	}
}

// Normalize fixes overlapping timestamps and flags gaps > 30s.
func (n *TimestampNormalizer) Normalize(transcript *artifacts.Transcript) (*artifacts.Transcript, error) {
	if transcript == nil {
		return nil, nil
	}
	if len(transcript.Segments) == 0 {
		return copyTranscript(transcript), nil
	}

	gapThreshold := n.GapThresholdSeconds
	if gapThreshold == 0 {
		gapThreshold = 30.0
	}

	result := copyTranscript(transcript)
	var segments []artifacts.Segment

	for i := 0; i < len(transcript.Segments); i++ {
		seg := transcript.Segments[i]
		segID := seg.ID

		// Fix overlapping timestamps: if this segment's end is after next segment's start,
		// adjust this segment's end to just before next segment starts
		if i+1 < len(transcript.Segments) {
			nextSeg := transcript.Segments[i+1]
			if seg.EndSeconds > nextSeg.StartSeconds {
				// Adjust current segment's end to just before next starts
				adjustedEnd := nextSeg.StartSeconds - 0.01
				if adjustedEnd < seg.StartSeconds {
					adjustedEnd = seg.StartSeconds
				}
				seg.EndSeconds = adjustedEnd
			}
		}

		// Append this segment exactly once, then a gap-marker segment if a long
		// silence follows before the next one. The gap branch must NOT also append
		// the segment (it previously did, then fell through to the unconditional
		// append below — duplicating every segment that preceded a >= threshold gap).
		segments = append(segments, copySegment(seg))

		if i+1 < len(transcript.Segments) {
			nextSeg := transcript.Segments[i+1]
			gap := nextSeg.StartSeconds - seg.EndSeconds
			if gap >= gapThreshold {
				gapSeg := artifacts.Segment{
					ID:           "gap_" + segID,
					SpeakerID:    seg.SpeakerID,
					StartSeconds: seg.EndSeconds,
					EndSeconds:   nextSeg.StartSeconds,
					SourceID:     seg.SourceID,
					SourceRole:   seg.SourceRole,
					Channel:      seg.Channel,
					Overlap:      false,
					Text:         "[potential gap: " + formatGapDuration(gap) + "]",
					Confidence:   nil,
					WordIDs:      []string{},
				}
				segments = append(segments, gapSeg)
			}
		}
	}

	result.Segments = segments
	return result, nil
}

func formatGapDuration(seconds float64) string {
	d := time.Duration(seconds * float64(time.Second))
	return d.Round(time.Second).String()
}

// ConfidenceNormalizer flags segments with average confidence < threshold
// and appends [low confidence] marker.
type ConfidenceNormalizer struct {
	// Threshold is the minimum acceptable confidence. Defaults to 0.7.
	Threshold float64
}

// NewConfidenceNormalizer creates a ConfidenceNormalizer with default settings.
func NewConfidenceNormalizer() *ConfidenceNormalizer {
	return &ConfidenceNormalizer{
		Threshold: 0.7,
	}
}

// Normalize marks segments with confidence below threshold.
func (n *ConfidenceNormalizer) Normalize(transcript *artifacts.Transcript) (*artifacts.Transcript, error) {
	if transcript == nil {
		return nil, nil
	}
	if len(transcript.Segments) == 0 {
		return copyTranscript(transcript), nil
	}

	threshold := n.Threshold
	if threshold == 0 {
		threshold = 0.7
	}

	result := copyTranscript(transcript)
	for i := range result.Segments {
		seg := &result.Segments[i]
		if seg.Confidence != nil && *seg.Confidence < threshold {
			if !strings.Contains(seg.Text, " [low confidence]") {
				seg.Text = seg.Text + " [low confidence]"
			}
		}
	}
	return result, nil
}

// FormatNormalizer fixes common ASR errors:
// - Repeated words: "the the the" -> "the"
// - Partial words ending in "-"
// - Filler words: um, uh, like in parentheses
type FormatNormalizer struct{}

var (
	// Word splitter for tokenizing on whitespace. Repeated-word collapse
	// happens in code because RE2 has no backreferences.
	wordSplitRE = regexp.MustCompile(`\s+`)
	// Partial word pattern: words ending with hyphen (incomplete)
	partialWordRE = regexp.MustCompile(`\b\w+-\s*`)
	// Filler words wrapped in parentheses rather than deleted, so the
	// hedging is visible but de-emphasized. fillerWords is the source list
	// the test reuses; fillerPattern is what Normalize actually applies.
	fillerWords   = []string{"um", "uh", "like"}
	fillerPattern = regexp.MustCompile(`(?i)\b(um|uh|like)\b`)
)

// collapseRepeats removes adjacent identical words ("the the" → "the"),
// case-insensitive. Replaces the `\b(\w+)\s+\1\b` backreference pattern
// that RE2 cannot compile.
func collapseRepeats(text string) string {
	tokens := wordSplitRE.Split(text, -1)
	if len(tokens) < 2 {
		return text
	}
	out := make([]string, 0, len(tokens))
	for _, tok := range tokens {
		if len(out) > 0 && strings.EqualFold(out[len(out)-1], tok) && tok != "" {
			continue
		}
		out = append(out, tok)
	}
	return strings.Join(out, " ")
}

// NewFormatNormalizer creates a FormatNormalizer.
func NewFormatNormalizer() *FormatNormalizer {
	return &FormatNormalizer{}
}

// Normalize applies format fixes to segment text.
func (f *FormatNormalizer) Normalize(transcript *artifacts.Transcript) (*artifacts.Transcript, error) {
	if transcript == nil {
		return nil, nil
	}
	if len(transcript.Segments) == 0 {
		return copyTranscript(transcript), nil
	}

	result := copyTranscript(transcript)
	for i := range result.Segments {
		seg := &result.Segments[i]
		text := seg.Text

		for {
			newText := collapseRepeats(text)
			if newText == text {
				break
			}
			text = newText
		}

		// Remove partial words (words ending with hyphen followed by space)
		text = partialWordRE.ReplaceAllString(text, "")

		text = fillerPattern.ReplaceAllStringFunc(text, func(match string) string {
			return "(" + match + ")"
		})

		seg.Text = strings.TrimSpace(text)
	}
	return result, nil
}

// PunctuationNormalizer adds punctuation based on prosodic features.
// It adds periods at the end of sentences lacking punctuation and capitalizes
// after sentence boundaries.
type PunctuationNormalizer struct{}

// capitalizeAfterRE matches the first word character after a sentence
// boundary so PunctuationNormalizer can upper-case it.
var capitalizeAfterRE = regexp.MustCompile(`([.!?])\s+(\w)`)

// NewPunctuationNormalizer creates a PunctuationNormalizer.
func NewPunctuationNormalizer() *PunctuationNormalizer {
	return &PunctuationNormalizer{}
}

// Normalize adds punctuation based on prosodic features.
func (p *PunctuationNormalizer) Normalize(transcript *artifacts.Transcript) (*artifacts.Transcript, error) {
	if transcript == nil {
		return nil, nil
	}
	if len(transcript.Segments) == 0 {
		return copyTranscript(transcript), nil
	}

	result := copyTranscript(transcript)
	for i := range result.Segments {
		seg := &result.Segments[i]
		trimmed := strings.TrimSpace(seg.Text)
		// Whitespace-only (or empty) text has no last rune; leave it
		// untouched rather than index past the start of the string.
		if trimmed == "" {
			continue
		}

		// Add a trailing period when the segment doesn't already end in
		// punctuation. Decode the final rune (not byte) so multibyte text
		// is classified correctly instead of inspecting a UTF-8 tail byte.
		lastChar, _ := utf8.DecodeLastRuneInString(trimmed)
		if !unicode.IsPunct(lastChar) && len(trimmed) > 3 {
			seg.Text = trimmed + "."
		} else {
			seg.Text = trimmed
		}

		// Capitalize the first letter after a sentence boundary.
		seg.Text = capitalizeAfterRE.ReplaceAllStringFunc(seg.Text, func(match string) string {
			parts := capitalizeAfterRE.FindStringSubmatch(match)
			if len(parts) >= 3 {
				return parts[1] + " " + strings.ToUpper(parts[2])
			}
			return match
		})
	}
	return result, nil
}

// SpeakerLabelNormalizer maps provider-specific labels to canonical speaker labels.
// The provider's speaker NUMBER is preserved, only the scheme and zero-padding are
// normalized: "SPEAKER_01" -> "speaker_1", "Guest 1" -> "speaker_1", "sp 02" ->
// "speaker_2". Stripping the zero-padding keeps the regex path's output in the SAME
// shape as the spk_ fallback below ("spk_0" -> "speaker_0"), so an unidentified
// speaker reads uniformly wherever the Label is shown.
type SpeakerLabelNormalizer struct {
	// ExplicitMappings allows overriding default mappings.
	ExplicitMappings map[string]string
}

var (
	// Default speaker label patterns in order of precedence
	defaultSpeakerPatterns = []struct {
		pattern *regexp.Regexp
		label   string
	}{
		{regexp.MustCompile(`(?i)^speaker[_\s]?(\d+)$`), "speaker_$1"},
		{regexp.MustCompile(`(?i)^guest[_\s]?(\d+)$`), "speaker_$1"},
		{regexp.MustCompile(`(?i)^participant[_\s]?(\d+)$`), "speaker_$1"},
		{regexp.MustCompile(`(?i)^sp[_\s]?(\d+)$`), "speaker_$1"},
		{regexp.MustCompile(`(?i)^user[_\s]?(\d+)$`), "speaker_$1"},
	}
)

// NewSpeakerLabelNormalizer creates a SpeakerLabelNormalizer.
func NewSpeakerLabelNormalizer() *SpeakerLabelNormalizer {
	return &SpeakerLabelNormalizer{
		ExplicitMappings: make(map[string]string),
	}
}

// Normalize maps provider-specific speaker labels to canonical form.
func (s *SpeakerLabelNormalizer) Normalize(transcript *artifacts.Transcript) (*artifacts.Transcript, error) {
	if transcript == nil {
		return nil, nil
	}

	result := copyTranscript(transcript)

	// Normalize only the human-readable Label. Speaker.ID is the stable
	// identity that segments reference, so it (and Segment.SpeakerID) must
	// not change here — rewriting segment references to labels while
	// leaving Speaker.ID untouched would make ValidateTranscript reject the
	// transcript ("segment references unknown speaker").
	for i := range result.Speakers {
		canonical := s.canonicalLabel(result.Speakers[i].ProviderLabel)
		if canonical == "" {
			if strings.HasPrefix(result.Speakers[i].ID, "spk_") {
				canonical = "speaker_" + result.Speakers[i].ID[len("spk_"):]
			} else {
				canonical = result.Speakers[i].ID
			}
		}
		result.Speakers[i].Label = canonical
	}

	return result, nil
}

func (s *SpeakerLabelNormalizer) canonicalLabel(providerLabel string) string {
	// Check explicit mappings first
	if explicit, ok := s.ExplicitMappings[providerLabel]; ok {
		return explicit
	}

	// Check default patterns. Re-emit the captured speaker number WITHOUT leading
	// zeros so "SPEAKER_01" and the spk_ fallback both yield "speaker_1" — one
	// uniform canonical shape regardless of how the provider zero-padded it.
	for _, entry := range defaultSpeakerPatterns {
		if m := entry.pattern.FindStringSubmatch(providerLabel); m != nil {
			return "speaker_" + stripLeadingZeros(m[1])
		}
	}

	return ""
}

// stripLeadingZeros normalizes a speaker index for the canonical label ("01" ->
// "1"), keeping a lone "0" as "0". Used so the label's number has one shape no
// matter how the provider padded it.
func stripLeadingZeros(num string) string {
	trimmed := strings.TrimLeft(num, "0")
	if trimmed == "" {
		return "0"
	}
	return trimmed
}

// TranscriptNormalizers is a chain of normalizers that applies each in sequence.
type TranscriptNormalizers []TranscriptNormalizer

// NewTranscriptNormalizers creates the default chain. It deliberately runs only
// the normalizers that are SAFE to apply to provider output that is then sent
// to the LLM and the search index verbatim:
//
//   - DiarizationNormalizer  — merges adjacent same-speaker turns (structural).
//   - SpeakerLabelNormalizer — canonicalizes the human-readable Label only;
//     never touches Segment.Text or the IDs segments reference.
//
// The remaining normalizers (Timestamp gap-flagging, Confidence "[low
// confidence]" tags, Format filler/partial-word edits, Punctuation) rewrite or
// inject markers INTO Segment.Text. The recognizer already returns punctuated,
// formatted text, so re-processing it corrupted real content (mangled
// hyphenated words, dropped "like", fabricated "[potential gap]" segments the
// model could cite) and leaked display markers into both the LLM prompt and FTS
// index. They remain available as standalone components for callers that want
// them, but are not in the default transcript path.
func NewTranscriptNormalizers() TranscriptNormalizers {
	return TranscriptNormalizers{
		NewDiarizationNormalizer(),
		NewSpeakerLabelNormalizer(),
	}
}

// Normalize applies each normalizer in sequence.
func (c TranscriptNormalizers) Normalize(transcript *artifacts.Transcript) (*artifacts.Transcript, error) {
	result := transcript
	for _, normalizer := range c {
		var err error
		result, err = normalizer.Normalize(result)
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

// copyTranscript creates a deep copy of a transcript.
func copyTranscript(t *artifacts.Transcript) *artifacts.Transcript {
	if t == nil {
		return nil
	}
	result := &artifacts.Transcript{
		SchemaVersion:   t.SchemaVersion,
		MeetingID:       t.MeetingID,
		Language:        t.Language,
		DurationSeconds: t.DurationSeconds,
		Provider:        t.Provider,
		Speakers:        make([]artifacts.Speaker, len(t.Speakers)),
		Segments:        make([]artifacts.Segment, len(t.Segments)),
		Words:           make([]artifacts.Word, len(t.Words)),
		Capabilities:    t.Capabilities,
	}
	copy(result.Speakers, t.Speakers)
	for i := range t.Segments {
		result.Segments[i] = copySegment(t.Segments[i])
	}
	copy(result.Words, t.Words)
	return result
}

// copySegment creates a copy of a segment.
func copySegment(s artifacts.Segment) artifacts.Segment {
	result := artifacts.Segment{
		ID:           s.ID,
		SpeakerID:    s.SpeakerID,
		StartSeconds: s.StartSeconds,
		EndSeconds:   s.EndSeconds,
		SourceID:     s.SourceID,
		SourceRole:   s.SourceRole,
		Channel:      s.Channel,
		Overlap:      s.Overlap,
		Text:         s.Text,
		WordIDs:      make([]string, len(s.WordIDs)),
	}
	if s.Confidence != nil {
		conf := *s.Confidence
		result.Confidence = &conf
	}
	copy(result.WordIDs, s.WordIDs)
	return result
}
