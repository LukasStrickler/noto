package speech

import "github.com/lukasstrickler/noto/internal/artifacts"

type Turn struct {
	SpeakerLabel string
	Start        float64
	End          float64
	Text         string
	SourceRole   string
}

type RawTranscript struct {
	ProviderID string
	JobID      string
	MeetingID  string
	Language   string
	Duration   float64
	Turns      []Turn
}

func NormalizeMistral(raw RawTranscript) (artifacts.Transcript, error) {
	raw.ProviderID = "mistral:voxtral-mini-transcribe"
	return normalize(raw)
}

func NormalizeAssemblyAI(raw RawTranscript) (artifacts.Transcript, error) {
	raw.ProviderID = "assemblyai:universal-3-pro"
	return normalize(raw)
}

func NormalizeElevenLabs(raw RawTranscript) (artifacts.Transcript, error) {
	raw.ProviderID = "elevenlabs:scribe_v2"
	return normalize(raw)
}

func normalize(raw RawTranscript) (artifacts.Transcript, error) {
	speakerIDs := map[string]string{}
	var speakers []artifacts.Speaker
	var segments []artifacts.Segment
	for i, turn := range raw.Turns {
		label := turn.SpeakerLabel
		if label == "" {
			label = "speaker"
		}
		speakerID, ok := speakerIDs[label]
		if !ok {
			speakerID = "spk_" + string(rune('0'+len(speakerIDs)))
			speakerIDs[label] = speakerID
			origin := "unknown"
			sourceID := ""
			if turn.SourceRole == "local_speaker" {
				origin = "local_speaker"
				sourceID = "src_mic"
			}
			if turn.SourceRole == "participants" {
				origin = "participant"
				sourceID = "src_system"
			}
			speakers = append(speakers, artifacts.Speaker{
				ID:              speakerID,
				Label:           "Speaker " + speakerID[len("spk_"):],
				Origin:          origin,
				DefaultSourceID: sourceID,
				ProviderLabel:   label,
			})
		}
		sourceRole := turn.SourceRole
		sourceID := ""
		channel := -1
		if sourceRole == "local_speaker" {
			sourceID = "src_mic"
			channel = 0
		}
		if sourceRole == "participants" {
			sourceID = "src_system"
			channel = 1
		}
		segments = append(segments, artifacts.Segment{
			ID:           segmentID(i),
			SpeakerID:    speakerID,
			StartSeconds: turn.Start,
			EndSeconds:   turn.End,
			SourceID:     sourceID,
			SourceRole:   sourceRole,
			Channel:      channel,
			Text:         turn.Text,
			WordIDs:      []string{},
		})
	}
	out := artifacts.Transcript{
		SchemaVersion:   "transcript.v1",
		MeetingID:       raw.MeetingID,
		Language:        raw.Language,
		DurationSeconds: raw.Duration,
		Provider: artifacts.TranscriptProvider{
			ID:    raw.ProviderID,
			JobID: raw.JobID,
		},
		Speakers: speakers,
		Segments: segments,
		Words:    []artifacts.Word{},
		Capabilities: artifacts.TranscriptCapabilities{
			WordTimestamps:     true,
			SpeakerDiarization: true,
			Channels:           true,
			SourceRoles:        true,
		},
	}
	return out, artifacts.ValidateTranscript(out)
}

func segmentID(i int) string {
	return "seg_" + leftPad(i+1, 6)
}

func leftPad(n int, width int) string {
	digits := "0123456789"
	if n == 0 {
		return "000000"[:width-1] + "0"
	}
	var rev []byte
	for n > 0 {
		rev = append(rev, digits[n%10])
		n /= 10
	}
	out := make([]byte, 0, width)
	for len(out)+len(rev) < width {
		out = append(out, '0')
	}
	for i := len(rev) - 1; i >= 0; i-- {
		out = append(out, rev[i])
	}
	return string(out)
}
