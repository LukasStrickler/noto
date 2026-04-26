package artifacts

import (
	"time"

	"github.com/lukasstrickler/noto/internal/notoerr"
)

type MeetingManifest struct {
	SchemaVersion     string             `json:"schema_version"`
	MeetingID         string             `json:"meeting_id"`
	CurrentVersionID  string             `json:"current_version_id"`
	Versions          []ManifestVersion  `json:"versions"`
}

type ManifestVersion struct {
	VersionID string    `json:"version_id"`
	CreatedAt time.Time `json:"created_at"`
	Reason    string    `json:"reason"`
	Checksum  string    `json:"checksum"`
}

type MeetingSource struct {
	Kind              string `json:"kind"`
	CaptureDevice     string `json:"capture_device,omitempty"`
	SourcePolicy      string `json:"source_policy,omitempty"`
	LocalSpeakerSource string `json:"local_speaker_source,omitempty"`
	ParticipantSource string `json:"participant_source,omitempty"`
}

type ProviderInfo struct {
	Transcription string `json:"transcription,omitempty"`
	Summary       string `json:"summary,omitempty"`
}

func (m *MeetingManifest) Kind() ArtifactKind {
	return KindMeeting
}

func (m *MeetingManifest) Validate() *notoerr.Error {
	if m.SchemaVersion != "manifest.v1" {
		return notoerr.New(ErrCodeValidationFailed, "schema_version must be manifest.v1", map[string]any{"schema_version": m.SchemaVersion})
	}
	if m.MeetingID == "" {
		return NewMissingFieldError("meeting_id")
	}
	if m.CurrentVersionID == "" {
		return NewMissingFieldError("current_version_id")
	}
	if len(m.Versions) == 0 {
		return NewMissingFieldError("versions")
	}
	foundCurrent := false
	for _, v := range m.Versions {
		if v.VersionID == "" {
			return NewMissingFieldError("versions.version_id")
		}
		if v.CreatedAt.IsZero() {
			return NewMissingFieldError("versions.created_at")
		}
		if v.Reason == "" {
			return NewMissingFieldError("versions.reason")
		}
		if v.VersionID == m.CurrentVersionID {
			foundCurrent = true
		}
	}
	if !foundCurrent {
		return notoerr.New(ErrCodeValidationFailed, "current_version_id not found in versions", map[string]any{"current_version_id": m.CurrentVersionID})
	}
	return nil
}
