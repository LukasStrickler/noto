package artifacts

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/lukasstrickler/noto/internal/notoerr"
)

func TestValidateManifest(t *testing.T) {
	t.Run("valid manifest", func(t *testing.T) {
		m := MeetingManifest{
			SchemaVersion:    "manifest.v1",
			MeetingID:        "mtg_123",
			CurrentVersionID: "ver_1",
			Versions: []ManifestVersion{
				{
					VersionID: "ver_1",
					CreatedAt: time.Now(),
					Reason:    "initial",
					Checksum:  "sha256:abc123",
				},
			},
		}
		if err := m.Validate(); err != nil {
			t.Errorf("expected no error, got %v", err)
		}
	})

	t.Run("missing schema_version", func(t *testing.T) {
		m := MeetingManifest{
			MeetingID:        "mtg_123",
			CurrentVersionID: "ver_1",
			Versions: []ManifestVersion{
				{VersionID: "ver_1", CreatedAt: time.Now(), Reason: "initial"},
			},
		}
		if err := m.Validate(); err == nil {
			t.Error("expected error for missing schema_version")
		}
	})

	t.Run("missing meeting_id", func(t *testing.T) {
		m := MeetingManifest{
			SchemaVersion:    "manifest.v1",
			CurrentVersionID: "ver_1",
			Versions: []ManifestVersion{
				{VersionID: "ver_1", CreatedAt: time.Now(), Reason: "initial"},
			},
		}
		if err := m.Validate(); err == nil {
			t.Error("expected error for missing meeting_id")
		}
	})

	t.Run("missing current_version_id", func(t *testing.T) {
		m := MeetingManifest{
			SchemaVersion: "manifest.v1",
			MeetingID:     "mtg_123",
			Versions: []ManifestVersion{
				{VersionID: "ver_1", CreatedAt: time.Now(), Reason: "initial"},
			},
		}
		if err := m.Validate(); err == nil {
			t.Error("expected error for missing current_version_id")
		}
	})

	t.Run("empty versions", func(t *testing.T) {
		m := MeetingManifest{
			SchemaVersion:    "manifest.v1",
			MeetingID:        "mtg_123",
			CurrentVersionID: "ver_1",
			Versions:         []ManifestVersion{},
		}
		if err := m.Validate(); err == nil {
			t.Error("expected error for empty versions")
		}
	})

	t.Run("current_version_id not in versions", func(t *testing.T) {
		m := MeetingManifest{
			SchemaVersion:    "manifest.v1",
			MeetingID:        "mtg_123",
			CurrentVersionID: "ver_2",
			Versions: []ManifestVersion{
				{VersionID: "ver_1", CreatedAt: time.Now(), Reason: "initial"},
			},
		}
		if err := m.Validate(); err == nil {
			t.Error("expected error for current_version_id not in versions")
		}
	})

	t.Run("missing version_id in versions", func(t *testing.T) {
		m := MeetingManifest{
			SchemaVersion:    "manifest.v1",
			MeetingID:        "mtg_123",
			CurrentVersionID: "ver_1",
			Versions: []ManifestVersion{
				{VersionID: "", CreatedAt: time.Now(), Reason: "initial"},
			},
		}
		if err := m.Validate(); err == nil {
			t.Error("expected error for missing version_id")
		}
	})
}

func TestValidateAudio(t *testing.T) {
	t.Run("valid audio", func(t *testing.T) {
		a := AudioMetadata{
			SchemaVersion:   "audio-asset.v1",
			MeetingID:       "mtg_123",
			AssetID:         "aud_123",
			DurationSeconds: 100.5,
			SampleRateHz:    48000,
			Channels:        2,
			Sources: []AudioSource{
				{ID: "src_1", Role: "local_speaker"},
			},
		}
		if err := a.Validate(); err != nil {
			t.Errorf("expected no error, got %v", err)
		}
	})

	t.Run("missing schema_version", func(t *testing.T) {
		a := AudioMetadata{
			MeetingID:       "mtg_123",
			AssetID:         "aud_123",
			DurationSeconds: 100.5,
			SampleRateHz:    48000,
			Channels:        2,
		}
		if err := a.Validate(); err == nil {
			t.Error("expected error for missing schema_version")
		}
	})

	t.Run("missing meeting_id", func(t *testing.T) {
		a := AudioMetadata{
			SchemaVersion:   "audio-asset.v1",
			AssetID:         "aud_123",
			DurationSeconds: 100.5,
			SampleRateHz:    48000,
			Channels:        2,
		}
		if err := a.Validate(); err == nil {
			t.Error("expected error for missing meeting_id")
		}
	})

	t.Run("invalid duration_seconds", func(t *testing.T) {
		a := AudioMetadata{
			SchemaVersion:   "audio-asset.v1",
			MeetingID:       "mtg_123",
			AssetID:         "aud_123",
			DurationSeconds: -1,
			SampleRateHz:    48000,
			Channels:        2,
		}
		if err := a.Validate(); err == nil {
			t.Error("expected error for invalid duration_seconds")
		}
	})

	t.Run("missing source id", func(t *testing.T) {
		a := AudioMetadata{
			SchemaVersion:   "audio-asset.v1",
			MeetingID:       "mtg_123",
			AssetID:         "aud_123",
			DurationSeconds: 100.5,
			SampleRateHz:    48000,
			Channels:        2,
			Sources: []AudioSource{
				{ID: "", Role: "local_speaker"},
			},
		}
		if err := a.Validate(); err == nil {
			t.Error("expected error for missing source id")
		}
	})
}

func TestValidateTranscript(t *testing.T) {
	conf := 0.95
	tests := []struct {
		name    string
		t       Transcript
		wantErr bool
	}{
		{
			name: "valid transcript",
			t: Transcript{
				SchemaVersion:   "transcript.v1",
				MeetingID:       "mtg_123",
				Provider:        TranscriptProvider{ID: "provider_1"},
				Speakers:        []Speaker{{ID: "spk_1", Label: "Speaker 1"}},
				Segments:        []Segment{{ID: "seg_1", SpeakerID: "spk_1", Text: "Hello", StartSeconds: 0, EndSeconds: 1}},
				Capabilities:    TranscriptCapabilities{WordTimestamps: true},
			},
			wantErr: false,
		},
		{
			name: "missing schema_version",
			t: Transcript{
				MeetingID:   "mtg_123",
				Provider:    TranscriptProvider{ID: "provider_1"},
				Speakers:    []Speaker{{ID: "spk_1"}},
				Segments:    []Segment{{ID: "seg_1", SpeakerID: "spk_1", Text: "Hello"}},
			},
			wantErr: true,
		},
		{
			name: "missing meeting_id",
			t: Transcript{
				SchemaVersion: "transcript.v1",
				Provider:      TranscriptProvider{ID: "provider_1"},
				Speakers:      []Speaker{{ID: "spk_1"}},
				Segments:      []Segment{{ID: "seg_1", SpeakerID: "spk_1", Text: "Hello"}},
			},
			wantErr: true,
		},
		{
			name: "missing provider id",
			t: Transcript{
				SchemaVersion: "transcript.v1",
				MeetingID:     "mtg_123",
				Speakers:      []Speaker{{ID: "spk_1"}},
				Segments:      []Segment{{ID: "seg_1", SpeakerID: "spk_1", Text: "Hello"}},
			},
			wantErr: true,
		},
		{
			name: "no speakers",
			t: Transcript{
				SchemaVersion: "transcript.v1",
				MeetingID:     "mtg_123",
				Provider:      TranscriptProvider{ID: "provider_1"},
				Speakers:      []Speaker{},
				Segments:      []Segment{{ID: "seg_1", SpeakerID: "spk_1", Text: "Hello"}},
			},
			wantErr: true,
		},
		{
			name: "no segments",
			t: Transcript{
				SchemaVersion: "transcript.v1",
				MeetingID:     "mtg_123",
				Provider:      TranscriptProvider{ID: "provider_1"},
				Speakers:      []Speaker{{ID: "spk_1"}},
				Segments:      []Segment{},
			},
			wantErr: true,
		},
		{
			name: "unknown speaker in segment",
			t: Transcript{
				SchemaVersion: "transcript.v1",
				MeetingID:     "mtg_123",
				Provider:      TranscriptProvider{ID: "provider_1"},
				Speakers:      []Speaker{{ID: "spk_1"}},
				Segments:      []Segment{{ID: "seg_1", SpeakerID: "spk_2", Text: "Hello"}},
			},
			wantErr: true,
		},
		{
			name: "segment end before start",
			t: Transcript{
				SchemaVersion: "transcript.v1",
				MeetingID:     "mtg_123",
				Provider:      TranscriptProvider{ID: "provider_1"},
				Speakers:      []Speaker{{ID: "spk_1"}},
				Segments:      []Segment{{ID: "seg_1", SpeakerID: "spk_1", Text: "Hello", StartSeconds: 5, EndSeconds: 1}},
			},
			wantErr: true,
		},
		{
			name: "segment missing id",
			t: Transcript{
				SchemaVersion: "transcript.v1",
				MeetingID:     "mtg_123",
				Provider:      TranscriptProvider{ID: "provider_1"},
				Speakers:      []Speaker{{ID: "spk_1"}},
				Segments:      []Segment{{SpeakerID: "spk_1", Text: "Hello"}},
			},
			wantErr: true,
		},
		{
			name: "segment missing text",
			t: Transcript{
				SchemaVersion: "transcript.v1",
				MeetingID:     "mtg_123",
				Provider:      TranscriptProvider{ID: "provider_1"},
				Speakers:      []Speaker{{ID: "spk_1"}},
				Segments:      []Segment{{ID: "seg_1", SpeakerID: "spk_1"}},
			},
			wantErr: true,
		},
		{
			name: "speaker missing id",
			t: Transcript{
				SchemaVersion: "transcript.v1",
				MeetingID:     "mtg_123",
				Provider:      TranscriptProvider{ID: "provider_1"},
				Speakers:      []Speaker{{Label: "Speaker 1"}},
				Segments:      []Segment{{ID: "seg_1", SpeakerID: "", Text: "Hello"}},
			},
			wantErr: true,
		},
		{
			name: "with confidence scores",
			t: Transcript{
				SchemaVersion:   "transcript.v1",
				MeetingID:       "mtg_123",
				Provider:        TranscriptProvider{ID: "provider_1"},
				Speakers:        []Speaker{{ID: "spk_1", Label: "Speaker 1"}},
				Segments:        []Segment{{ID: "seg_1", SpeakerID: "spk_1", Text: "Hello", StartSeconds: 0, EndSeconds: 1, Confidence: &conf}},
				Capabilities:    TranscriptCapabilities{WordTimestamps: true},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateTranscript(tt.t)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateTranscript() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateSummary(t *testing.T) {
	tests := []struct {
		name    string
		s       Summary
		t       Transcript
		wantErr bool
	}{
		{
			name: "valid summary",
			s: Summary{
				SchemaVersion: "summary.v1",
				MeetingID:     "mtg_123",
				Decisions:     []SummaryItem{{Text: "Decision 1"}},
			},
			t:       Transcript{},
			wantErr: false,
		},
		{
			name: "missing schema_version",
			s: Summary{
				MeetingID: "mtg_123",
			},
			t:       Transcript{},
			wantErr: true,
		},
		{
			name: "missing meeting_id",
			s: Summary{
				SchemaVersion: "summary.v1",
			},
			t:       Transcript{},
			wantErr: true,
		},
		{
			name: "meeting_id mismatch with transcript",
			s: Summary{
				SchemaVersion: "summary.v1",
				MeetingID:     "mtg_123",
			},
			t:       Transcript{MeetingID: "mtg_456"},
			wantErr: true,
		},
		{
			name: "evidence segment not in transcript",
			s: Summary{
				SchemaVersion: "summary.v1",
				MeetingID:     "mtg_123",
				Decisions: []SummaryItem{
					{
						Text: "Decision 1",
						Evidence: []Evidence{
							{SegmentID: "seg_unknown", Quote: "quote"},
						},
					},
				},
			},
			t: Transcript{
				MeetingID: "mtg_123",
				Segments:  []Segment{{ID: "seg_1", Text: "Hello"}},
			},
			wantErr: true,
		},
		{
			name: "valid summary with transcript matching",
			s: Summary{
				SchemaVersion: "summary.v1",
				MeetingID:     "mtg_123",
				Decisions: []SummaryItem{
					{
						Text: "Decision 1",
						Evidence: []Evidence{
							{SegmentID: "seg_1", Quote: "quote"},
						},
					},
				},
			},
			t: Transcript{
				MeetingID: "mtg_123",
				Segments:  []Segment{{ID: "seg_1", Text: "Hello"}},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateSummary(tt.s, tt.t)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateSummary() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidatePromptVersion(t *testing.T) {
	t.Run("valid prompt version", func(t *testing.T) {
		p := PromptVersion{
			SchemaVersion: "prompt.v1",
			Version:       "v1",
			PromptID:      "summary.v1",
			Content:       "You are a summarizer...",
		}
		if err := p.Validate(); err != nil {
			t.Errorf("expected no error, got %v", err)
		}
	})

	t.Run("missing schema_version", func(t *testing.T) {
		p := PromptVersion{
			Version:  "v1",
			PromptID: "summary.v1",
		}
		if err := p.Validate(); err == nil {
			t.Error("expected error for missing schema_version")
		}
	})

	t.Run("missing version", func(t *testing.T) {
		p := PromptVersion{
			SchemaVersion: "prompt.v1",
			PromptID:      "summary.v1",
		}
		if err := p.Validate(); err == nil {
			t.Error("expected error for missing version")
		}
	})

	t.Run("missing prompt_id", func(t *testing.T) {
		p := PromptVersion{
			SchemaVersion: "prompt.v1",
			Version:       "v1",
		}
		if err := p.Validate(); err == nil {
			t.Error("expected error for missing prompt_id")
		}
	})
}

func TestChecksum(t *testing.T) {
	t.Run("compute checksum", func(t *testing.T) {
		data := []byte(`{"test":"data"}`)
		checksum := ComputeChecksum(data)
		if checksum == "" {
			t.Error("expected non-empty checksum")
		}
		if checksum[:7] != "sha256:" {
			t.Errorf("expected sha256: prefix, got %s", checksum[:7])
		}
	})

	t.Run("parse checksum", func(t *testing.T) {
		checksum := "sha256:abc123"
		algo, hash, err := ParseChecksum(checksum)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if algo != "sha256" {
			t.Errorf("expected algo sha256, got %s", algo)
		}
		if hash != "abc123" {
			t.Errorf("expected hash abc123, got %s", hash)
		}
	})

	t.Run("parse invalid checksum", func(t *testing.T) {
		checksum := "md5:abc123"
		_, _, err := ParseChecksum(checksum)
		if err == nil {
			t.Error("expected error for invalid checksum prefix")
		}
	})

	t.Run("verify checksum", func(t *testing.T) {
		data := []byte(`{"test":"data"}`)
		checksum := ComputeChecksum(data)
		err := VerifyChecksum(data, checksum)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("verify checksum mismatch", func(t *testing.T) {
		data := []byte(`{"test":"data"}`)
		err := VerifyChecksum(data, "sha256:wrong")
		if err == nil {
			t.Error("expected error for checksum mismatch")
		}
	})

	t.Run("canonical json", func(t *testing.T) {
		v := map[string]any{"b": "2", "a": "1"}
		canonical, err := CanonicalJSON(v)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		var result map[string]any
		if err := json.Unmarshal(canonical, &result); err != nil {
			t.Errorf("unmarshal error: %v", err)
		}
		keys := make([]string, 0, len(result))
		for k := range result {
			keys = append(keys, k)
		}
		if len(keys) != 2 || keys[0] != "a" || keys[1] != "b" {
			t.Errorf("expected sorted keys [a b], got %v", keys)
		}
	})
}

func TestArtifactKind(t *testing.T) {
	t.Run("manifest kind", func(t *testing.T) {
		m := MeetingManifest{}
		if m.Kind() != KindMeeting {
			t.Errorf("expected KindMeeting, got %s", m.Kind())
		}
	})

	t.Run("audio kind", func(t *testing.T) {
		a := AudioMetadata{}
		if a.Kind() != KindAudio {
			t.Errorf("expected KindAudio, got %s", a.Kind())
		}
	})

	t.Run("transcript kind", func(t *testing.T) {
		t_struct := Transcript{}
		if t_struct.Kind() != KindTranscript {
			t.Errorf("expected KindTranscript, got %s", t_struct.Kind())
		}
	})

	t.Run("summary kind", func(t *testing.T) {
		s := Summary{}
		if s.Kind() != KindSummary {
			t.Errorf("expected KindSummary, got %s", s.Kind())
		}
	})

	t.Run("prompt version kind", func(t *testing.T) {
		p := PromptVersion{}
		if p.Kind() != KindPrompt {
			t.Errorf("expected KindPrompt, got %s", p.Kind())
		}
	})
}

func TestTranscriptKindAndVersion(t *testing.T) {
	t_struct := Transcript{SchemaVersion: "transcript.v1"}
	if t_struct.Kind() != KindTranscript {
		t.Errorf("expected KindTranscript, got %s", t_struct.Kind())
	}
	if t_struct.Version() != "transcript.v1" {
		t.Errorf("expected transcript.v1, got %s", t_struct.Version())
	}
}

func TestSummaryKindAndVersion(t *testing.T) {
	s := Summary{SchemaVersion: "summary.v1"}
	if s.Kind() != KindSummary {
		t.Errorf("expected KindSummary, got %s", s.Kind())
	}
	if s.Version() != "summary.v1" {
		t.Errorf("expected summary.v1, got %s", s.Version())
	}
}

func TestMeetingManifestKindAndVersion(t *testing.T) {
	m := MeetingManifest{SchemaVersion: "manifest.v1"}
	if m.Kind() != KindMeeting {
		t.Errorf("expected KindMeeting, got %s", m.Kind())
	}
	if m.Version() != "manifest.v1" {
		t.Errorf("expected manifest.v1, got %s", m.Version())
	}
}

func TestAudioMetadataKindAndVersion(t *testing.T) {
	a := AudioMetadata{SchemaVersion: "audio-asset.v1"}
	if a.Kind() != KindAudio {
		t.Errorf("expected KindAudio, got %s", a.Kind())
	}
	if a.Version() != "audio-asset.v1" {
		t.Errorf("expected audio-asset.v1, got %s", a.Version())
	}
}

func TestPromptVersionKindAndVersion(t *testing.T) {
	p := PromptVersion{SchemaVersion: "prompt.v1"}
	if p.Kind() != KindPrompt {
		t.Errorf("expected KindPrompt, got %s", p.Kind())
	}
	if p.Version() != "prompt.v1" {
		t.Errorf("expected prompt.v1, got %s", p.Version())
	}
}
