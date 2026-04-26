package artifacts

import (
	"time"

	"github.com/lukasstrickler/noto/internal/notoerr"
)

type AudioMetadata struct {
	SchemaVersion   string          `json:"schema_version"`
	MeetingID       string          `json:"meeting_id"`
	AssetID         string          `json:"asset_id"`
	Path            string          `json:"path"`
	Format          string          `json:"format"`
	Codec           string          `json:"codec"`
	DurationSeconds float64         `json:"duration_seconds"`
	Channels        int             `json:"channels"`
	SampleRateHz    int             `json:"sample_rate_hz"`
	Sources         []AudioSource   `json:"sources"`
	SizeBytes       int64           `json:"size_bytes"`
	SHA256          string          `json:"sha256"`
	Retention       AudioRetention  `json:"retention"`
}

type AudioSource struct {
	ID          string `json:"id"`
	Role        string `json:"role"`
	Label       string `json:"label"`
	Channel     int    `json:"channel"`
	DeviceName  string `json:"device_name,omitempty"`
}

type AudioRetention struct {
	Policy    string     `json:"policy"`
	DeletedAt *time.Time `json:"deleted_at,omitempty"`
	Retained  bool       `json:"retained"`
}

func (a *AudioMetadata) Kind() ArtifactKind {
	return KindAudio
}

func (a *AudioMetadata) Validate() *notoerr.Error {
	if a.SchemaVersion != "audio-asset.v1" {
		return notoerr.New(ErrCodeValidationFailed, "schema_version must be audio-asset.v1", map[string]any{"schema_version": a.SchemaVersion})
	}
	if a.MeetingID == "" {
		return NewMissingFieldError("meeting_id")
	}
	if a.AssetID == "" {
		return NewMissingFieldError("asset_id")
	}
	if a.DurationSeconds <= 0 {
		return NewInvalidFieldError("duration_seconds", "duration_seconds must be positive")
	}
	if a.SampleRateHz <= 0 {
		return NewInvalidFieldError("sample_rate_hz", "sample_rate_hz must be positive")
	}
	if a.Channels <= 0 {
		return NewInvalidFieldError("channels", "channels must be positive")
	}
	for _, src := range a.Sources {
		if src.ID == "" {
			return NewMissingFieldError("sources.id")
		}
		if src.Role == "" {
			return NewMissingFieldError("sources.role")
		}
	}
	return nil
}
