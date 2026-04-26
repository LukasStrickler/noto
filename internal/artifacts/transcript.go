package artifacts

import "github.com/lukasstrickler/noto/internal/notoerr"

type Transcript struct {
	SchemaVersion   string                 `json:"schema_version"`
	MeetingID       string                 `json:"meeting_id"`
	Language        string                 `json:"language"`
	DurationSeconds float64                `json:"duration_seconds"`
	Provider        TranscriptProvider     `json:"provider"`
	Speakers        []Speaker              `json:"speakers"`
	Segments        []Segment              `json:"segments"`
	Words           []Word                 `json:"words"`
	Capabilities    TranscriptCapabilities `json:"capabilities"`
}

type TranscriptProvider struct {
	ID             string `json:"id"`
	JobID          string `json:"job_id"`
	RawResponseRef string `json:"raw_response_ref"`
}

type Speaker struct {
	ID              string `json:"id"`
	Label           string `json:"label"`
	Origin          string `json:"origin"`
	DefaultSourceID string `json:"default_source_id"`
	DisplayName     string `json:"display_name"`
	ProviderLabel   string `json:"provider_label"`
}

type Segment struct {
	ID           string   `json:"id"`
	SpeakerID    string   `json:"speaker_id"`
	StartSeconds float64  `json:"start_seconds"`
	EndSeconds   float64  `json:"end_seconds"`
	SourceID     string   `json:"source_id"`
	SourceRole   string   `json:"source_role"`
	Channel      int      `json:"channel"`
	Overlap      bool     `json:"overlap"`
	Text         string   `json:"text"`
	Confidence   *float64 `json:"confidence"`
	WordIDs      []string `json:"word_ids"`
}

type Word struct {
	ID                string   `json:"id"`
	SegmentID         string   `json:"segment_id"`
	SpeakerID         string   `json:"speaker_id"`
	StartSeconds      float64  `json:"start_seconds"`
	EndSeconds        float64  `json:"end_seconds"`
	SourceID          string   `json:"source_id"`
	SourceRole        string   `json:"source_role"`
	Text              string   `json:"text"`
	Confidence        *float64 `json:"confidence"`
	SpeakerConfidence *float64 `json:"speaker_confidence"`
}

type TranscriptCapabilities struct {
	WordTimestamps     bool `json:"word_timestamps"`
	SpeakerDiarization bool `json:"speaker_diarization"`
	OverlapDetection   bool `json:"overlap_detection"`
	Channels           bool `json:"channels"`
	SourceRoles        bool `json:"source_roles"`
}

func (t *Transcript) Kind() ArtifactKind {
	return KindTranscript
}

func (t *Transcript) Validate() *notoerr.Error {
	if t.SchemaVersion != "transcript.v1" {
		return notoerr.New(ErrCodeValidationFailed, "schema_version must be transcript.v1", map[string]any{"schema_version": t.SchemaVersion})
	}
	if t.MeetingID == "" {
		return NewMissingFieldError("meeting_id")
	}
	if t.Provider.ID == "" {
		return NewMissingFieldError("provider.id")
	}
	if len(t.Speakers) == 0 {
		return NewMissingFieldError("speakers")
	}
	if len(t.Segments) == 0 {
		return NewMissingFieldError("segments")
	}
	speakers := map[string]bool{}
	for _, speaker := range t.Speakers {
		if speaker.ID == "" {
			return NewMissingFieldError("speakers.id")
		}
		speakers[speaker.ID] = true
	}
	for _, segment := range t.Segments {
		if segment.ID == "" {
			return NewMissingFieldError("segments.id")
		}
		if segment.Text == "" {
			return NewMissingFieldError("segments.text")
		}
		if !speakers[segment.SpeakerID] {
			return notoerr.New(ErrCodeValidationFailed, "segment references unknown speaker", map[string]any{"segment_id": segment.ID, "speaker_id": segment.SpeakerID})
		}
		if segment.EndSeconds < segment.StartSeconds {
			return notoerr.New(ErrCodeValidationFailed, "segment end_seconds before start_seconds", map[string]any{"segment_id": segment.ID})
		}
	}
	return nil
}

func ValidateTranscript(t Transcript) *notoerr.Error {
	return t.Validate()
}
