package speech

import (
	"strings"
	"testing"

	"github.com/lukasstrickler/noto/internal/artifacts"
)

func TestNormalizersProduceValidTranscriptV1(t *testing.T) {
	raw := RawTranscript{
		MeetingID: "mtg_test",
		JobID:     "job_123",
		Language:  "en",
		Duration:  12,
		Turns: []Turn{
			{SpeakerLabel: "A", Start: 0, End: 4, Text: "Let's choose a provider.", SourceRole: "local_speaker"},
			{SpeakerLabel: "B", Start: 4.2, End: 8, Text: "OpenRouter should handle the summary.", SourceRole: "participants"},
		},
	}
	cases := []struct {
		name string
		fn   func(RawTranscript) (artifacts.Transcript, error)
		want string
	}{
		{"mistral", NormalizeMistral, "mistral:"},
		{"assemblyai", NormalizeAssemblyAI, "assemblyai:"},
		{"elevenlabs", NormalizeElevenLabs, "elevenlabs:"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.fn(raw)
			if err != nil {
				t.Fatalf("normalizer returned error: %v", err)
			}
			if err := artifacts.ValidateTranscript(got); err != nil {
				t.Fatalf("ValidateTranscript returned error: %v", err)
			}
			if !strings.HasPrefix(got.Provider.ID, tc.want) {
				t.Fatalf("Provider.ID = %s, want prefix %s", got.Provider.ID, tc.want)
			}
			if got.Segments[0].SourceRole != "local_speaker" || got.Segments[1].SourceRole != "participants" {
				t.Fatalf("source roles were not preserved: %+v", got.Segments)
			}
		})
	}
}
