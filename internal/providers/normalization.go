package providers

import (
	"github.com/lukasstrickler/noto/internal/artifacts"
	"github.com/lukasstrickler/noto/internal/providers/speech"
)

const MinConfidenceThreshold = 0.7

func NormalizeTranscript(raw *artifacts.Transcript) (*artifacts.Transcript, error) {
	if raw == nil {
		return nil, nil
	}

	normalizers := speech.NewTranscriptNormalizers()
	normalized, err := normalizers.Normalize(raw)
	if err != nil {
		return nil, err
	}

	for i := range normalized.Speakers {
		sp := &normalized.Speakers[i]
		if sp.ID == "" {
			continue
		}
		if sp.Origin == "unknown" || sp.Origin == "" {
			if sp.ProviderLabel != "" {
				sp.Origin = normalizeOrigin(sp.ProviderLabel)
			}
		}
	}

	return normalized, nil
}

func normalizeOrigin(providerLabel string) string {
	providerLabel = normalizeSpeakerLabel(providerLabel)
	switch {
	case contains(providerLabel, "speaker_"):
		return "participant"
	case contains(providerLabel, "mic"):
		return "local_speaker"
	case contains(providerLabel, "system"):
		return "participant"
	default:
		return "unknown"
	}
}

func normalizeSpeakerLabel(label string) string {
	switch {
	case label == "":
		return "speaker_1"
	default:
		return label
	}
}

func NormalizeSpeakerLabels(transcript *artifacts.Transcript) *artifacts.Transcript {
	if transcript == nil {
		return nil
	}

	speakerNum := 1
	speakerMap := map[string]string{}

	result := &artifacts.Transcript{
		SchemaVersion:   transcript.SchemaVersion,
		MeetingID:       transcript.MeetingID,
		Language:        transcript.Language,
		DurationSeconds: transcript.DurationSeconds,
		Provider:        transcript.Provider,
		Speakers:        make([]artifacts.Speaker, 0, len(transcript.Speakers)),
		Segments:        make([]artifacts.Segment, len(transcript.Segments)),
		Words:           transcript.Words,
		Capabilities:    transcript.Capabilities,
	}

	for _, speaker := range transcript.Speakers {
		newLabel := speaker.Label
		if newLabel == "" {
			newLabel = speaker.ProviderLabel
		}
		if newLabel == "" {
			newLabel = "speaker_" + itoa(speakerNum)
		}

		canonicalLabel := canonicalizeSpeakerLabel(newLabel)
		if _, exists := speakerMap[canonicalLabel]; !exists {
			speakerMap[canonicalLabel] = "speaker_" + itoa(speakerNum)
			speakerNum++
		}

		normalized := artifacts.Speaker{
			ID:            speakerMap[canonicalLabel],
			Label:         canonicalLabel,
			Origin:        speaker.Origin,
			DefaultSourceID: speaker.DefaultSourceID,
			DisplayName:   speaker.DisplayName,
			ProviderLabel: speaker.ProviderLabel,
		}
		result.Speakers = append(result.Speakers, normalized)
	}

	for i, seg := range transcript.Segments {
		originalLabel := seg.SpeakerID
		if sp, ok := findSpeakerByID(transcript.Speakers, originalLabel); ok {
			originalLabel = sp.Label
			if originalLabel == "" {
				originalLabel = sp.ProviderLabel
			}
		}
		if originalLabel == "" {
			originalLabel = "speaker_1"
		}

		canonicalLabel := canonicalizeSpeakerLabel(originalLabel)
		newSpeakerID, exists := speakerMap[canonicalLabel]
		if !exists {
			newSpeakerID = "speaker_" + itoa(speakerNum)
			speakerMap[canonicalLabel] = newSpeakerID
			speakerNum++
		}

		result.Segments[i] = seg
		result.Segments[i].SpeakerID = newSpeakerID
	}

	return result
}

func canonicalizeSpeakerLabel(label string) string {
	if label == "" {
		return "speaker_1"
	}

	switch {
	case len(label) > 8 && label[:8] == "speaker_":
		return label
	case len(label) > 4 && label[:4] == "spk_":
		return "speaker_" + label[4:]
	default:
		return label
	}
}

func findSpeakerByID(speakers []artifacts.Speaker, id string) (artifacts.Speaker, bool) {
	for _, sp := range speakers {
		if sp.ID == id {
			return sp, true
		}
	}
	return artifacts.Speaker{}, false
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := "0123456789"
	var result []byte
	for n > 0 {
		result = append([]byte{digits[n%10]}, result...)
		n /= 10
	}
	return string(result)
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
