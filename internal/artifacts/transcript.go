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

func ValidateTranscript(t Transcript) error {
	if t.SchemaVersion != "transcript.v1" {
		return notoerr.New("schema_validation_failed", "Transcript schema_version must be transcript.v1.", map[string]any{"schema_version": t.SchemaVersion})
	}
	if t.MeetingID == "" {
		return notoerr.New("schema_validation_failed", "Transcript meeting_id is required.", map[string]any{"field": "meeting_id"})
	}
	if t.Provider.ID == "" {
		return notoerr.New("schema_validation_failed", "Transcript provider.id is required.", map[string]any{"field": "provider.id"})
	}
	if len(t.Speakers) == 0 {
		return notoerr.New("schema_validation_failed", "Transcript must include at least one speaker.", map[string]any{"field": "speakers"})
	}
	if len(t.Segments) == 0 {
		return notoerr.New("schema_validation_failed", "Transcript must include at least one segment.", map[string]any{"field": "segments"})
	}
	speakers := map[string]bool{}
	for _, speaker := range t.Speakers {
		if speaker.ID == "" {
			return notoerr.New("schema_validation_failed", "Speaker id is required.", map[string]any{"field": "speakers.id"})
		}
		speakers[speaker.ID] = true
	}
	for _, segment := range t.Segments {
		if segment.ID == "" || segment.Text == "" {
			return notoerr.New("schema_validation_failed", "Segment id and text are required.", map[string]any{"field": "segments"})
		}
		if !speakers[segment.SpeakerID] {
			return notoerr.New("schema_validation_failed", "Segment references an unknown speaker.", map[string]any{"segment_id": segment.ID, "speaker_id": segment.SpeakerID})
		}
		if segment.EndSeconds < segment.StartSeconds {
			return notoerr.New("schema_validation_failed", "Segment end_seconds cannot be before start_seconds.", map[string]any{"segment_id": segment.ID})
		}
	}
	return nil
}
