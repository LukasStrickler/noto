package speech

import (
	"strings"
	"testing"

	"github.com/lukasstrickler/noto/internal/artifacts"
)

func makeTestTranscript() *artifacts.Transcript {
	return &artifacts.Transcript{
		SchemaVersion:   "transcript.v1",
		MeetingID:       "mtg_test",
		Language:        "en",
		DurationSeconds: 60.0,
		Provider: artifacts.TranscriptProvider{
			ID:    "test:provider",
			JobID: "job_123",
		},
		Speakers: []artifacts.Speaker{
			{ID: "spk_0", Label: "Speaker 0", Origin: "local_speaker", DefaultSourceID: "src_mic", ProviderLabel: "SPEAKER_01"},
			{ID: "spk_1", Label: "Speaker 1", Origin: "participant", DefaultSourceID: "src_system", ProviderLabel: "Guest 1"},
		},
		Segments: []artifacts.Segment{
			{ID: "seg_000001", SpeakerID: "spk_0", StartSeconds: 0.0, EndSeconds: 5.0, SourceID: "src_mic", SourceRole: "local_speaker", Channel: 0, Overlap: false, Text: "First segment", Confidence: floatPtr(0.95)},
			{ID: "seg_000002", SpeakerID: "spk_0", StartSeconds: 5.1, EndSeconds: 10.0, SourceID: "src_mic", SourceRole: "local_speaker", Channel: 0, Overlap: false, Text: "Second segment", Confidence: floatPtr(0.90)},
			{ID: "seg_000003", SpeakerID: "spk_1", StartSeconds: 10.1, EndSeconds: 15.0, SourceID: "src_system", SourceRole: "participants", Channel: 1, Overlap: false, Text: "Third segment", Confidence: floatPtr(0.85)},
			{ID: "seg_000004", SpeakerID: "spk_1", StartSeconds: 15.1, EndSeconds: 20.0, SourceID: "src_system", SourceRole: "participants", Channel: 1, Overlap: false, Text: "Fourth segment", Confidence: floatPtr(0.50)},
			{ID: "seg_000005", SpeakerID: "spk_0", StartSeconds: 60.0, EndSeconds: 65.0, SourceID: "src_mic", SourceRole: "local_speaker", Channel: 0, Overlap: false, Text: "Fifth segment after gap", Confidence: floatPtr(0.92)},
		},
		Words: []artifacts.Word{},
		Capabilities: artifacts.TranscriptCapabilities{
			WordTimestamps:     true,
			SpeakerDiarization: true,
			Channels:           true,
			SourceRoles:        true,
		},
	}
}

func floatPtr(f float64) *float64 {
	return &f
}

func TestDiarizationNormalizer_MergeAdjacentSameSpeaker(t *testing.T) {
	norm := NewDiarizationNormalizer()
	transcript := makeTestTranscript()

	result, err := norm.Normalize(transcript)
	if err != nil {
		t.Fatalf("Normalize returned error: %v", err)
	}

	if len(result.Segments) >= len(transcript.Segments) {
		t.Fatalf("expected merged segments, got %d (was %d)", len(result.Segments), len(transcript.Segments))
	}

	foundSpeaker0 := false
	for _, seg := range result.Segments {
		if seg.SpeakerID == "spk_0" {
			foundSpeaker0 = true
		}
	}
	if !foundSpeaker0 {
		t.Error("expected at least one segment with spk_0")
	}
}

func TestDiarizationNormalizer_RespectsSpeakerChanges(t *testing.T) {
	norm := NewDiarizationNormalizer()
	transcript := makeTestTranscript()

	result, err := norm.Normalize(transcript)
	if err != nil {
		t.Fatalf("Normalize returned error: %v", err)
	}

	hasSpk0 := false
	hasSpk1 := false
	for _, seg := range result.Segments {
		if seg.SpeakerID == "spk_0" {
			hasSpk0 = true
		}
		if seg.SpeakerID == "spk_1" {
			hasSpk1 = true
		}
	}

	if !hasSpk0 || !hasSpk1 {
		t.Error("expected segments from both speakers after normalization")
	}
}

func TestDiarizationNormalizer_MergesText(t *testing.T) {
	transcript := &artifacts.Transcript{
		SchemaVersion:   "transcript.v1",
		MeetingID:       "mtg_test",
		Language:        "en",
		DurationSeconds: 60.0,
		Provider:        artifacts.TranscriptProvider{ID: "test"},
		Speakers: []artifacts.Speaker{
			{ID: "spk_0", Label: "Speaker 0", Origin: "local_speaker", DefaultSourceID: "src_mic", ProviderLabel: "SPEAKER_01"},
		},
		Segments: []artifacts.Segment{
			{ID: "seg_000001", SpeakerID: "spk_0", StartSeconds: 0.0, EndSeconds: 2.0, SourceID: "src_mic", SourceRole: "local_speaker", Channel: 0, Overlap: false, Text: "Hello"},
			{ID: "seg_000002", SpeakerID: "spk_0", StartSeconds: 2.1, EndSeconds: 4.0, SourceID: "src_mic", SourceRole: "local_speaker", Channel: 0, Overlap: false, Text: "World"},
		},
		Words: []artifacts.Word{},
		Capabilities: artifacts.TranscriptCapabilities{
			WordTimestamps: true, SpeakerDiarization: true, Channels: true, SourceRoles: true,
		},
	}

	norm := NewDiarizationNormalizer()
	result, err := norm.Normalize(transcript)
	if err != nil {
		t.Fatalf("Normalize returned error: %v", err)
	}

	if len(result.Segments) != 1 {
		t.Fatalf("expected 1 merged segment, got %d", len(result.Segments))
	}

	if !strings.Contains(result.Segments[0].Text, "Hello") || !strings.Contains(result.Segments[0].Text, "World") {
		t.Errorf("expected merged text containing 'Hello' and 'World', got '%s'", result.Segments[0].Text)
	}
}

func TestDiarizationNormalizer_GapThreshold(t *testing.T) {
	transcript := &artifacts.Transcript{
		SchemaVersion:   "transcript.v1",
		MeetingID:       "mtg_test",
		Language:        "en",
		DurationSeconds: 60.0,
		Provider:        artifacts.TranscriptProvider{ID: "test"},
		Speakers: []artifacts.Speaker{
			{ID: "spk_0", Label: "Speaker 0", Origin: "local_speaker", DefaultSourceID: "src_mic", ProviderLabel: "SPEAKER_01"},
		},
		Segments: []artifacts.Segment{
			{ID: "seg_000001", SpeakerID: "spk_0", StartSeconds: 0.0, EndSeconds: 2.0, SourceID: "src_mic", SourceRole: "local_speaker", Channel: 0, Overlap: false, Text: "First"},
			{ID: "seg_000002", SpeakerID: "spk_0", StartSeconds: 5.0, EndSeconds: 7.0, SourceID: "src_mic", SourceRole: "local_speaker", Channel: 0, Overlap: false, Text: "Second"},
		},
		Words: []artifacts.Word{},
		Capabilities: artifacts.TranscriptCapabilities{
			WordTimestamps: true, SpeakerDiarization: true, Channels: true, SourceRoles: true,
		},
	}

	norm := NewDiarizationNormalizer()
	norm.GapThresholdSeconds = 0.5

	result, err := norm.Normalize(transcript)
	if err != nil {
		t.Fatalf("Normalize returned error: %v", err)
	}

	if len(result.Segments) != 2 {
		t.Errorf("expected 2 separate segments (gap too large), got %d", len(result.Segments))
	}
}

func TestDiarizationNormalizer_EmptySegments(t *testing.T) {
	norm := NewDiarizationNormalizer()
	transcript := &artifacts.Transcript{
		SchemaVersion:   "transcript.v1",
		MeetingID:       "mtg_test",
		Language:        "en",
		DurationSeconds: 60.0,
		Provider:        artifacts.TranscriptProvider{ID: "test"},
		Speakers:        []artifacts.Speaker{},
		Segments:        []artifacts.Segment{},
		Words:           []artifacts.Word{},
		Capabilities:    artifacts.TranscriptCapabilities{},
	}

	result, err := norm.Normalize(transcript)
	if err != nil {
		t.Fatalf("Normalize returned error: %v", err)
	}
	if len(result.Segments) != 0 {
		t.Errorf("expected 0 segments, got %d", len(result.Segments))
	}
}

func TestDiarizationNormalizer_NilTranscript(t *testing.T) {
	norm := NewDiarizationNormalizer()
	result, err := norm.Normalize(nil)
	if err != nil {
		t.Fatalf("Normalize returned error: %v", err)
	}
	if result != nil {
		t.Error("expected nil result for nil input")
	}
}

func TestTimestampNormalizer_FixesOverlap(t *testing.T) {
	transcript := &artifacts.Transcript{
		SchemaVersion:   "transcript.v1",
		MeetingID:       "mtg_test",
		Language:        "en",
		DurationSeconds: 60.0,
		Provider:        artifacts.TranscriptProvider{ID: "test"},
		Speakers: []artifacts.Speaker{
			{ID: "spk_0", Label: "Speaker 0", Origin: "local_speaker", DefaultSourceID: "src_mic", ProviderLabel: "SPEAKER_01"},
		},
		Segments: []artifacts.Segment{
			{ID: "seg_000001", SpeakerID: "spk_0", StartSeconds: 0.0, EndSeconds: 5.0, SourceID: "src_mic", SourceRole: "local_speaker", Channel: 0, Overlap: false, Text: "First"},
			{ID: "seg_000002", SpeakerID: "spk_0", StartSeconds: 4.0, EndSeconds: 8.0, SourceID: "src_mic", SourceRole: "local_speaker", Channel: 0, Overlap: false, Text: "Second"},
		},
		Words: []artifacts.Word{},
		Capabilities: artifacts.TranscriptCapabilities{
			WordTimestamps: true, SpeakerDiarization: true, Channels: true, SourceRoles: true,
		},
	}

	norm := NewTimestampNormalizer()
	result, err := norm.Normalize(transcript)
	if err != nil {
		t.Fatalf("Normalize returned error: %v", err)
	}

	if result.Segments[0].EndSeconds >= result.Segments[1].StartSeconds {
		t.Error("expected first segment end <= next segment start after fixing overlap")
	}
}

func TestTimestampNormalizer_FlagsGap(t *testing.T) {
	transcript := &artifacts.Transcript{
		SchemaVersion:   "transcript.v1",
		MeetingID:       "mtg_test",
		Language:        "en",
		DurationSeconds: 60.0,
		Provider:        artifacts.TranscriptProvider{ID: "test"},
		Speakers: []artifacts.Speaker{
			{ID: "spk_0", Label: "Speaker 0", Origin: "local_speaker", DefaultSourceID: "src_mic", ProviderLabel: "SPEAKER_01"},
		},
		Segments: []artifacts.Segment{
			{ID: "seg_000001", SpeakerID: "spk_0", StartSeconds: 0.0, EndSeconds: 5.0, SourceID: "src_mic", SourceRole: "local_speaker", Channel: 0, Overlap: false, Text: "First"},
			{ID: "seg_000002", SpeakerID: "spk_0", StartSeconds: 40.0, EndSeconds: 45.0, SourceID: "src_mic", SourceRole: "local_speaker", Channel: 0, Overlap: false, Text: "Second"},
		},
		Words: []artifacts.Word{},
		Capabilities: artifacts.TranscriptCapabilities{
			WordTimestamps: true, SpeakerDiarization: true, Channels: true, SourceRoles: true,
		},
	}

	norm := NewTimestampNormalizer()
	result, err := norm.Normalize(transcript)
	if err != nil {
		t.Fatalf("Normalize returned error: %v", err)
	}

	foundGap := false
	for _, seg := range result.Segments {
		if strings.Contains(seg.Text, "[potential gap:") {
			foundGap = true
			break
		}
	}
	if !foundGap {
		t.Error("expected gap marker segment for gap > 30s")
	}
}

func TestTimestampNormalizer_NoSmallGapFlagged(t *testing.T) {
	transcript := &artifacts.Transcript{
		SchemaVersion:   "transcript.v1",
		MeetingID:       "mtg_test",
		Language:        "en",
		DurationSeconds: 60.0,
		Provider:        artifacts.TranscriptProvider{ID: "test"},
		Speakers: []artifacts.Speaker{
			{ID: "spk_0", Label: "Speaker 0", Origin: "local_speaker", DefaultSourceID: "src_mic", ProviderLabel: "SPEAKER_01"},
		},
		Segments: []artifacts.Segment{
			{ID: "seg_000001", SpeakerID: "spk_0", StartSeconds: 0.0, EndSeconds: 5.0, SourceID: "src_mic", SourceRole: "local_speaker", Channel: 0, Overlap: false, Text: "First"},
			{ID: "seg_000002", SpeakerID: "spk_0", StartSeconds: 6.0, EndSeconds: 10.0, SourceID: "src_mic", SourceRole: "local_speaker", Channel: 0, Overlap: false, Text: "Second"},
		},
		Words: []artifacts.Word{},
		Capabilities: artifacts.TranscriptCapabilities{
			WordTimestamps: true, SpeakerDiarization: true, Channels: true, SourceRoles: true,
		},
	}

	norm := NewTimestampNormalizer()
	result, err := norm.Normalize(transcript)
	if err != nil {
		t.Fatalf("Normalize returned error: %v", err)
	}

	for _, seg := range result.Segments {
		if strings.Contains(seg.Text, "[potential gap:") {
			t.Error("expected no gap marker for gap < 30s")
		}
	}
}

func TestConfidenceNormalizer_FlagsLowConfidence(t *testing.T) {
	transcript := &artifacts.Transcript{
		SchemaVersion:   "transcript.v1",
		MeetingID:       "mtg_test",
		Language:        "en",
		DurationSeconds: 60.0,
		Provider:        artifacts.TranscriptProvider{ID: "test"},
		Speakers: []artifacts.Speaker{
			{ID: "spk_0", Label: "Speaker 0", Origin: "local_speaker", DefaultSourceID: "src_mic", ProviderLabel: "SPEAKER_01"},
		},
		Segments: []artifacts.Segment{
			{ID: "seg_000001", SpeakerID: "spk_0", StartSeconds: 0.0, EndSeconds: 5.0, SourceID: "src_mic", SourceRole: "local_speaker", Channel: 0, Overlap: false, Text: "Low conf text", Confidence: floatPtr(0.5)},
			{ID: "seg_000002", SpeakerID: "spk_0", StartSeconds: 5.0, EndSeconds: 10.0, SourceID: "src_mic", SourceRole: "local_speaker", Channel: 0, Overlap: false, Text: "High conf text", Confidence: floatPtr(0.9)},
		},
		Words: []artifacts.Word{},
		Capabilities: artifacts.TranscriptCapabilities{
			WordTimestamps: true, SpeakerDiarization: true, Channels: true, SourceRoles: true,
		},
	}

	norm := NewConfidenceNormalizer()
	result, err := norm.Normalize(transcript)
	if err != nil {
		t.Fatalf("Normalize returned error: %v", err)
	}

	if !strings.Contains(result.Segments[0].Text, "[low confidence]") {
		t.Error("expected [low confidence] marker on segment with confidence < 0.7")
	}
	if strings.Contains(result.Segments[1].Text, "[low confidence]") {
		t.Error("did not expect [low confidence] marker on segment with confidence >= 0.7")
	}
}

func TestConfidenceNormalizer_CustomThreshold(t *testing.T) {
	transcript := &artifacts.Transcript{
		SchemaVersion:   "transcript.v1",
		MeetingID:       "mtg_test",
		Language:        "en",
		DurationSeconds: 60.0,
		Provider:        artifacts.TranscriptProvider{ID: "test"},
		Speakers: []artifacts.Speaker{
			{ID: "spk_0", Label: "Speaker 0", Origin: "local_speaker", DefaultSourceID: "src_mic", ProviderLabel: "SPEAKER_01"},
		},
		Segments: []artifacts.Segment{
			{ID: "seg_000001", SpeakerID: "spk_0", StartSeconds: 0.0, EndSeconds: 5.0, SourceID: "src_mic", SourceRole: "local_speaker", Channel: 0, Overlap: false, Text: "Medium conf text", Confidence: floatPtr(0.75)},
		},
		Words: []artifacts.Word{},
		Capabilities: artifacts.TranscriptCapabilities{
			WordTimestamps: true, SpeakerDiarization: true, Channels: true, SourceRoles: true,
		},
	}

	norm := NewConfidenceNormalizer()
	norm.Threshold = 0.8

	result, err := norm.Normalize(transcript)
	if err != nil {
		t.Fatalf("Normalize returned error: %v", err)
	}

	if !strings.Contains(result.Segments[0].Text, "[low confidence]") {
		t.Error("expected [low confidence] marker with custom threshold 0.8")
	}
}

func TestConfidenceNormalizer_NilConfidence(t *testing.T) {
	transcript := &artifacts.Transcript{
		SchemaVersion:   "transcript.v1",
		MeetingID:       "mtg_test",
		Language:        "en",
		DurationSeconds: 60.0,
		Provider:        artifacts.TranscriptProvider{ID: "test"},
		Speakers: []artifacts.Speaker{
			{ID: "spk_0", Label: "Speaker 0", Origin: "local_speaker", DefaultSourceID: "src_mic", ProviderLabel: "SPEAKER_01"},
		},
		Segments: []artifacts.Segment{
			{ID: "seg_000001", SpeakerID: "spk_0", StartSeconds: 0.0, EndSeconds: 5.0, SourceID: "src_mic", SourceRole: "local_speaker", Channel: 0, Overlap: false, Text: "No confidence score", Confidence: nil},
		},
		Words: []artifacts.Word{},
		Capabilities: artifacts.TranscriptCapabilities{
			WordTimestamps: true, SpeakerDiarization: true, Channels: true, SourceRoles: true,
		},
	}

	norm := NewConfidenceNormalizer()
	result, err := norm.Normalize(transcript)
	if err != nil {
		t.Fatalf("Normalize returned error: %v", err)
	}

	if strings.Contains(result.Segments[0].Text, "[low confidence]") {
		t.Error("did not expect [low confidence] marker when confidence is nil")
	}
}

func TestFormatNormalizer_RemovesRepeatedWords(t *testing.T) {
	transcript := &artifacts.Transcript{
		SchemaVersion:   "transcript.v1",
		MeetingID:       "mtg_test",
		Language:        "en",
		DurationSeconds: 60.0,
		Provider:        artifacts.TranscriptProvider{ID: "test"},
		Speakers: []artifacts.Speaker{
			{ID: "spk_0", Label: "Speaker 0", Origin: "local_speaker", DefaultSourceID: "src_mic", ProviderLabel: "SPEAKER_01"},
		},
		Segments: []artifacts.Segment{
			{ID: "seg_000001", SpeakerID: "spk_0", StartSeconds: 0.0, EndSeconds: 5.0, SourceID: "src_mic", SourceRole: "local_speaker", Channel: 0, Overlap: false, Text: "The the the problem is is is fixed"},
		},
		Words: []artifacts.Word{},
		Capabilities: artifacts.TranscriptCapabilities{
			WordTimestamps: true, SpeakerDiarization: true, Channels: true, SourceRoles: true,
		},
	}

	norm := NewFormatNormalizer()
	result, err := norm.Normalize(transcript)
	if err != nil {
		t.Fatalf("Normalize returned error: %v", err)
	}

	text := result.Segments[0].Text
	if strings.Count(strings.ToLower(text), "the ") > 1 {
		t.Errorf("expected at most one 'the' in output, got '%s'", text)
	}
	if strings.Count(strings.ToLower(text), "is ") > 1 {
		t.Errorf("expected at most one 'is' in output, got '%s'", text)
	}
}

func TestFormatNormalizer_RemovesPartialWords(t *testing.T) {
	transcript := &artifacts.Transcript{
		SchemaVersion:   "transcript.v1",
		MeetingID:       "mtg_test",
		Language:        "en",
		DurationSeconds: 60.0,
		Provider:        artifacts.TranscriptProvider{ID: "test"},
		Speakers: []artifacts.Speaker{
			{ID: "spk_0", Label: "Speaker 0", Origin: "local_speaker", DefaultSourceID: "src_mic", ProviderLabel: "SPEAKER_01"},
		},
		Segments: []artifacts.Segment{
			{ID: "seg_000001", SpeakerID: "spk_0", StartSeconds: 0.0, EndSeconds: 5.0, SourceID: "src_mic", SourceRole: "local_speaker", Channel: 0, Overlap: false, Text: "We discussed this prev- we need to proceed"},
		},
		Words: []artifacts.Word{},
		Capabilities: artifacts.TranscriptCapabilities{
			WordTimestamps: true, SpeakerDiarization: true, Channels: true, SourceRoles: true,
		},
	}

	norm := NewFormatNormalizer()
	result, err := norm.Normalize(transcript)
	if err != nil {
		t.Fatalf("Normalize returned error: %v", err)
	}

	if strings.Contains(result.Segments[0].Text, "prev-") {
		t.Errorf("expected partial word 'prev-' to be removed, got '%s'", result.Segments[0].Text)
	}
}

func TestFormatNormalizer_FillerWords(t *testing.T) {
	transcript := &artifacts.Transcript{
		SchemaVersion:   "transcript.v1",
		MeetingID:       "mtg_test",
		Language:        "en",
		DurationSeconds: 60.0,
		Provider:        artifacts.TranscriptProvider{ID: "test"},
		Speakers: []artifacts.Speaker{
			{ID: "spk_0", Label: "Speaker 0", Origin: "local_speaker", DefaultSourceID: "src_mic", ProviderLabel: "SPEAKER_01"},
		},
		Segments: []artifacts.Segment{
			{ID: "seg_000001", SpeakerID: "spk_0", StartSeconds: 0.0, EndSeconds: 5.0, SourceID: "src_mic", SourceRole: "local_speaker", Channel: 0, Overlap: false, Text: "Um I think uh we should like proceed with the plan"},
		},
		Words: []artifacts.Word{},
		Capabilities: artifacts.TranscriptCapabilities{
			WordTimestamps: true, SpeakerDiarization: true, Channels: true, SourceRoles: true,
		},
	}

	norm := NewFormatNormalizer()
	result, err := norm.Normalize(transcript)
	if err != nil {
		t.Fatalf("Normalize returned error: %v", err)
	}

	text := strings.ToLower(result.Segments[0].Text)
	for _, filler := range fillerWords {
		if strings.Contains(text, filler+" ") || strings.HasSuffix(text, filler) {
			t.Errorf("expected filler '%s' to be parenthesized or removed, got '%s'", filler, result.Segments[0].Text)
		}
	}
}

func TestPunctuationNormalizer_AddsPeriod(t *testing.T) {
	transcript := &artifacts.Transcript{
		SchemaVersion:   "transcript.v1",
		MeetingID:       "mtg_test",
		Language:        "en",
		DurationSeconds: 60.0,
		Provider:        artifacts.TranscriptProvider{ID: "test"},
		Speakers: []artifacts.Speaker{
			{ID: "spk_0", Label: "Speaker 0", Origin: "local_speaker", DefaultSourceID: "src_mic", ProviderLabel: "SPEAKER_01"},
		},
		Segments: []artifacts.Segment{
			{ID: "seg_000001", SpeakerID: "spk_0", StartSeconds: 0.0, EndSeconds: 5.0, SourceID: "src_mic", SourceRole: "local_speaker", Channel: 0, Overlap: false, Text: "Let's start"},
		},
		Words: []artifacts.Word{},
		Capabilities: artifacts.TranscriptCapabilities{
			WordTimestamps: true, SpeakerDiarization: true, Channels: true, SourceRoles: true,
		},
	}

	norm := NewPunctuationNormalizer()
	result, err := norm.Normalize(transcript)
	if err != nil {
		t.Fatalf("Normalize returned error: %v", err)
	}

	if !strings.HasSuffix(result.Segments[0].Text, ".") {
		t.Errorf("expected period added to text without punctuation, got '%s'", result.Segments[0].Text)
	}
}

func TestPunctuationNormalizer_PreservesExistingPunctuation(t *testing.T) {
	transcript := &artifacts.Transcript{
		SchemaVersion:   "transcript.v1",
		MeetingID:       "mtg_test",
		Language:        "en",
		DurationSeconds: 60.0,
		Provider:        artifacts.TranscriptProvider{ID: "test"},
		Speakers: []artifacts.Speaker{
			{ID: "spk_0", Label: "Speaker 0", Origin: "local_speaker", DefaultSourceID: "src_mic", ProviderLabel: "SPEAKER_01"},
		},
		Segments: []artifacts.Segment{
			{ID: "seg_000001", SpeakerID: "spk_0", StartSeconds: 0.0, EndSeconds: 5.0, SourceID: "src_mic", SourceRole: "local_speaker", Channel: 0, Overlap: false, Text: "Let's start!"},
		},
		Words: []artifacts.Word{},
		Capabilities: artifacts.TranscriptCapabilities{
			WordTimestamps: true, SpeakerDiarization: true, Channels: true, SourceRoles: true,
		},
	}

	norm := NewPunctuationNormalizer()
	result, err := norm.Normalize(transcript)
	if err != nil {
		t.Fatalf("Normalize returned error: %v", err)
	}

	if result.Segments[0].Text != "Let's start!" {
		t.Errorf("expected existing punctuation preserved, got '%s'", result.Segments[0].Text)
	}
}

func TestPunctuationNormalizer_CapitalizesAfterSentenceBoundary(t *testing.T) {
	transcript := &artifacts.Transcript{
		SchemaVersion:   "transcript.v1",
		MeetingID:       "mtg_test",
		Language:        "en",
		DurationSeconds: 60.0,
		Provider:        artifacts.TranscriptProvider{ID: "test"},
		Speakers: []artifacts.Speaker{
			{ID: "spk_0", Label: "Speaker 0", Origin: "local_speaker", DefaultSourceID: "src_mic", ProviderLabel: "SPEAKER_01"},
		},
		Segments: []artifacts.Segment{
			{ID: "seg_000001", SpeakerID: "spk_0", StartSeconds: 0.0, EndSeconds: 5.0, SourceID: "src_mic", SourceRole: "local_speaker", Channel: 0, Overlap: false, Text: "first sentence. second starts here"},
		},
		Words: []artifacts.Word{},
		Capabilities: artifacts.TranscriptCapabilities{
			WordTimestamps: true, SpeakerDiarization: true, Channels: true, SourceRoles: true,
		},
	}

	norm := NewPunctuationNormalizer()
	result, err := norm.Normalize(transcript)
	if err != nil {
		t.Fatalf("Normalize returned error: %v", err)
	}

	if !strings.Contains(result.Segments[0].Text, "Second") {
		t.Errorf("expected 'second' capitalized after period, got '%s'", result.Segments[0].Text)
	}
}

func TestSpeakerLabelNormalizer_MapsSPEAKERLabels(t *testing.T) {
	transcript := &artifacts.Transcript{
		SchemaVersion:   "transcript.v1",
		MeetingID:       "mtg_test",
		Language:        "en",
		DurationSeconds: 60.0,
		Provider:        artifacts.TranscriptProvider{ID: "test"},
		Speakers: []artifacts.Speaker{
			{ID: "spk_0", Label: "Speaker 0", Origin: "local_speaker", DefaultSourceID: "src_mic", ProviderLabel: "SPEAKER_01"},
			{ID: "spk_1", Label: "Speaker 1", Origin: "participant", DefaultSourceID: "src_system", ProviderLabel: "Guest 1"},
		},
		Segments: []artifacts.Segment{
			{ID: "seg_000001", SpeakerID: "spk_0", StartSeconds: 0.0, EndSeconds: 5.0, SourceID: "src_mic", SourceRole: "local_speaker", Channel: 0, Overlap: false, Text: "First"},
			{ID: "seg_000002", SpeakerID: "spk_1", StartSeconds: 5.0, EndSeconds: 10.0, SourceID: "src_system", SourceRole: "participants", Channel: 1, Overlap: false, Text: "Second"},
		},
		Words: []artifacts.Word{},
		Capabilities: artifacts.TranscriptCapabilities{
			WordTimestamps: true, SpeakerDiarization: true, Channels: true, SourceRoles: true,
		},
	}

	norm := NewSpeakerLabelNormalizer()
	result, err := norm.Normalize(transcript)
	if err != nil {
		t.Fatalf("Normalize returned error: %v", err)
	}

	if result.Speakers[0].Label == "SPEAKER_01" {
		t.Errorf("expected SPEAKER_01 to be normalized, got '%s'", result.Speakers[0].Label)
	}
}

func TestSpeakerLabelNormalizer_ExplicitMappings(t *testing.T) {
	transcript := &artifacts.Transcript{
		SchemaVersion:   "transcript.v1",
		MeetingID:       "mtg_test",
		Language:        "en",
		DurationSeconds: 60.0,
		Provider:        artifacts.TranscriptProvider{ID: "test"},
		Speakers: []artifacts.Speaker{
			{ID: "spk_0", Label: "Speaker 0", Origin: "local_speaker", DefaultSourceID: "src_mic", ProviderLabel: "JOHN"},
		},
		Segments: []artifacts.Segment{
			{ID: "seg_000001", SpeakerID: "spk_0", StartSeconds: 0.0, EndSeconds: 5.0, SourceID: "src_mic", SourceRole: "local_speaker", Channel: 0, Overlap: false, Text: "First"},
		},
		Words: []artifacts.Word{},
		Capabilities: artifacts.TranscriptCapabilities{
			WordTimestamps: true, SpeakerDiarization: true, Channels: true, SourceRoles: true,
		},
	}

	norm := NewSpeakerLabelNormalizer()
	norm.ExplicitMappings["JOHN"] = "John Doe"

	result, err := norm.Normalize(transcript)
	if err != nil {
		t.Fatalf("Normalize returned error: %v", err)
	}

	if result.Speakers[0].Label != "John Doe" {
		t.Errorf("expected explicit mapping 'John Doe', got '%s'", result.Speakers[0].Label)
	}
}

func TestNopNormalizer(t *testing.T) {
	norm := &NopNormalizer{}
	transcript := makeTestTranscript()

	result, err := norm.Normalize(transcript)
	if err != nil {
		t.Fatalf("Normalize returned error: %v", err)
	}

	if result != transcript {
		t.Error("NopNormalizer should return same transcript")
	}
}

func TestTranscriptNormalizersChain(t *testing.T) {
	transcript := &artifacts.Transcript{
		SchemaVersion:   "transcript.v1",
		MeetingID:       "mtg_test",
		Language:        "en",
		DurationSeconds: 60.0,
		Provider:        artifacts.TranscriptProvider{ID: "test"},
		Speakers: []artifacts.Speaker{
			{ID: "speaker_1", Label: "Speaker 1", Origin: "local_speaker", DefaultSourceID: "src_mic", ProviderLabel: "SPEAKER_01"},
		},
		Segments: []artifacts.Segment{
			{ID: "seg_000001", SpeakerID: "speaker_1", StartSeconds: 0.0, EndSeconds: 2.0, SourceID: "src_mic", SourceRole: "local_speaker", Channel: 0, Overlap: false, Text: "Hello", Confidence: floatPtr(0.95)},
			{ID: "seg_000002", SpeakerID: "speaker_1", StartSeconds: 2.1, EndSeconds: 4.0, SourceID: "src_mic", SourceRole: "local_speaker", Channel: 0, Overlap: false, Text: "Hello", Confidence: floatPtr(0.90)},
		},
		Words: []artifacts.Word{},
		Capabilities: artifacts.TranscriptCapabilities{
			WordTimestamps: true, SpeakerDiarization: true, Channels: true, SourceRoles: true,
		},
	}

	chain := NewTranscriptNormalizers()
	result, err := chain.Normalize(transcript)
	if err != nil {
		t.Fatalf("Normalize returned error: %v", err)
	}

	if len(result.Segments) >= len(transcript.Segments) {
		t.Error("expected chain to merge segments")
	}
}

func TestTranscriptNormalizersChain_DoesNotModifyOriginal(t *testing.T) {
	transcript := &artifacts.Transcript{
		SchemaVersion:   "transcript.v1",
		MeetingID:       "mtg_test",
		Language:        "en",
		DurationSeconds: 60.0,
		Provider:        artifacts.TranscriptProvider{ID: "test"},
		Speakers: []artifacts.Speaker{
			{ID: "speaker_1", Label: "Speaker 1", Origin: "local_speaker", DefaultSourceID: "src_mic", ProviderLabel: "SPEAKER_01"},
		},
		Segments: []artifacts.Segment{
			{ID: "seg_000001", SpeakerID: "speaker_1", StartSeconds: 0.0, EndSeconds: 2.0, SourceID: "src_mic", SourceRole: "local_speaker", Channel: 0, Overlap: false, Text: "Hello", Confidence: floatPtr(0.5)},
			{ID: "seg_000002", SpeakerID: "speaker_1", StartSeconds: 2.1, EndSeconds: 4.0, SourceID: "src_mic", SourceRole: "local_speaker", Channel: 0, Overlap: false, Text: "Hello", Confidence: floatPtr(0.5)},
		},
		Words: []artifacts.Word{},
		Capabilities: artifacts.TranscriptCapabilities{
			WordTimestamps: true, SpeakerDiarization: true, Channels: true, SourceRoles: true,
		},
	}

	originalText := transcript.Segments[0].Text
	originalConf := *transcript.Segments[0].Confidence

	chain := NewTranscriptNormalizers()
	result, err := chain.Normalize(transcript)
	if err != nil {
		t.Fatalf("Normalize returned error: %v", err)
	}

	if transcript.Segments[0].Text == result.Segments[0].Text {
		t.Error("expected original transcript to be preserved (not modified in-place)")
	}
	if transcript.Segments[0].Text != originalText {
		t.Errorf("original text should be unchanged, was '%s'", transcript.Segments[0].Text)
	}
	if *transcript.Segments[0].Confidence != originalConf {
		t.Error("original confidence should be unchanged")
	}
}

func TestCopyTranscript(t *testing.T) {
	original := makeTestTranscript()
	copy := copyTranscript(original)

	if copy == original {
		t.Error("copy should be a different pointer")
	}

	if len(copy.Segments) != len(original.Segments) {
		t.Error("segment count should match")
	}

	for i := range original.Segments {
		if &copy.Segments[i] == &original.Segments[i] {
			t.Error("segment slice should be a different allocation")
		}
	}
}
