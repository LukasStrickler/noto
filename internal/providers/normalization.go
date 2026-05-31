package providers

import (
	"strings"

	"github.com/lukasstrickler/noto/internal/artifacts"
	"github.com/lukasstrickler/noto/internal/providers/speech"
)

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
	case strings.Contains(providerLabel, "speaker_"):
		return "participant"
	case strings.Contains(providerLabel, "mic"):
		return "local_speaker"
	case strings.Contains(providerLabel, "system"):
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
