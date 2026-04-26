package speech

import (
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/lukasstrickler/noto/internal/artifacts"
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
			// Start first segment
			newSeg := copySegment(seg)
			current = &newSeg
			continue
		}

		// Check if we can merge: same speaker and within gap threshold
		if current.SpeakerID == seg.SpeakerID {
			gapBetween := seg.StartSeconds - current.EndSeconds
			if gapBetween >= 0 && gapBetween <= gap {
				// Merge: extend current segment
				current.EndSeconds = seg.EndSeconds
				current.Text = current.Text + " " + seg.Text
				// Combine word IDs
				current.WordIDs = append(current.WordIDs, seg.WordIDs...)
				// Update confidence to average if both have confidence
				if current.Confidence != nil && seg.Confidence != nil {
					avg := (*current.Confidence + *seg.Confidence) / 2
					current.Confidence = &avg
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
	return &result, nil
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

		// Check for gap > gapThreshold before next segment
		if i+1 < len(transcript.Segments) {
			nextSeg := transcript.Segments[i+1]
			gap := nextSeg.StartSeconds - seg.EndSeconds
			if gap > gapThreshold {
				// Add the original segment first
				segCopy := copySegment(seg)
				segments = append(segments, segCopy)

				// Insert gap marker segment
				gapSeg := artifacts.Segment{
					ID:           "gap_" + segID,
					SpeakerID:    seg.SpeakerID,
					StartSeconds: seg.EndSeconds,
					EndSeconds:   nextSeg.StartSeconds,
					SourceID:     seg.SourceID,
					SourceRole:   seg.SourceRole,
					Channel:      seg.Channel,
					Overlap:      false,
					Text:        "[potential gap: " + formatGapDuration(gap) + "]",
					Confidence:   nil,
					WordIDs:      []string{},
				}
				segments = append(segments, gapSeg)
				continue
			}
		}

		segments = append(segments, copySegment(seg))
	}

	result.Segments = segments
	return &result, nil
}

func formatGapDuration(seconds float64) string {
	d := time.Duration(seconds * float64(time.Second))
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	if h > 0 {
		return time.Duration(d).Round(time.Second).String()
	}
	if m > 0 {
		return time.Duration(d).Round(time.Second).String()
	}
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
			seg.Text = seg.Text + " [low confidence]"
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
	// Repeated word pattern: matches same word appearing twice in a row (case-insensitive)
	repeatedWordRE = regexp.MustCompile(`\b(\w+)\s+\1\b`)
	// Partial word pattern: words ending with hyphen (incomplete)
	partialWordRE = regexp.MustCompile(`\b\w+-\s*`)
	// Filler words to remove: um, uh, like
	fillerWords   = []string{"um", "uh", "like"}
	fillerPattern = regexp.MustCompile(`\b(um|uh|like)\b`)
)

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

		// Remove repeated words (case-insensitive)
		text = repeatedWordRE.ReplaceAllStringFunc(text, func(match string) string {
			// Extract the word (without the space and repeat)
			parts := repeatedWordRE.FindStringSubmatch(match)
			if len(parts) >= 2 {
				return parts[1]
			}
			return match
		})

		// Remove partial words (words ending with hyphen followed by space)
		text = partialWordRE.ReplaceAllString(text, "")

		// Remove filler words (case-insensitive) and add parentheses
		text = fillerPattern.ReplaceAllStringFunc(strings.ToLower(text), func(match string) string {
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

var (
	// Sentence ending patterns (low confidence indicators)
	sentenceEndings   = []string{".", "!", "?", ":", ";"}
	capitalizeAfterRE = regexp.MustCompile(`([.!?])\s+(\w)`)
)

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
		text := seg.Text

		// Skip if already ends with punctuation
		if len(text) > 0 {
			lastChar := rune(text[len(text)-1])
			if !unicode.IsPunct(lastChar) {
				// Text doesn't end with punctuation — check if it should
				// Add period if text has substantial content and no punctuation
				trimmed := strings.TrimSpace(text)
				if len(trimmed) > 3 {
					seg.Text = trimmed + "."
				}
			}

			// Capitalize after sentence boundaries
			seg.Text = capitalizeAfterRE.ReplaceAllStringFunc(seg.Text, func(match string) string {
				parts := capitalizeAfterRE.FindStringSubmatch(match)
				if len(parts) >= 3 {
					return parts[1] + " " + strings.ToUpper(parts[2])
				}
				return match
			})
		}
	}
	return result, nil
}

// SpeakerLabelNormalizer maps provider-specific labels to canonical speaker labels.
// For example: "SPEAKER_01" -> "speaker_1", "Guest 1" -> "speaker_2"
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

	// Build speaker mapping
	speakerMap := make(map[string]string)
	for _, speaker := range transcript.Speakers {
		canonicalLabel := s.canonicalLabel(speaker.ProviderLabel)
		if canonicalLabel == "" {
			canonicalLabel = "speaker_" + speaker.ID[len("spk_"):]
		}
		speakerMap[speaker.ID] = canonicalLabel
	}

	// Update speaker labels in result
	for i := range result.Speakers {
		canonical := s.canonicalLabel(result.Speakers[i].ProviderLabel)
		if canonical == "" {
			canonical = "speaker_" + result.Speakers[i].ID[len("spk_"):]
		}
		result.Speakers[i].Label = canonical
	}

	// Update segment speaker references
	for i := range result.Segments {
		if newLabel, ok := speakerMap[result.Segments[i].SpeakerID]; ok {
			result.Segments[i].SpeakerID = newLabel
		}
	}

	return result, nil
}

func (s *SpeakerLabelNormalizer) canonicalLabel(providerLabel string) string {
	// Check explicit mappings first
	if explicit, ok := s.ExplicitMappings[providerLabel]; ok {
		return explicit
	}

	// Check default patterns
	for _, entry := range defaultSpeakerPatterns {
		if entry.pattern.MatchString(providerLabel) {
			return entry.pattern.ReplaceAllString(providerLabel, entry.label)
		}
	}

	return ""
}

// TranscriptNormalizers is a chain of normalizers that applies each in sequence.
type TranscriptNormalizers []TranscriptNormalizer

// NewTranscriptNormalizers creates a new chain with the default normalizers.
func NewTranscriptNormalizers() TranscriptNormalizers {
	return TranscriptNormalizers{
		NewDiarizationNormalizer(),
		NewTimestampNormalizer(),
		NewConfidenceNormalizer(),
		NewFormatNormalizer(),
		NewPunctuationNormalizer(),
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
