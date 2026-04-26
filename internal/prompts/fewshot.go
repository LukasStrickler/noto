package prompts

import (
	"fmt"
	"strings"
)

var FewShotDecisionExamples = []DecisionExample{
	{
		Name: "Architecture Decision",
		Transcript: `[seg_000210] Speaker 1: "I think post-meeting diarization is the right approach for V1."
[seg_000211] Speaker 0: "Agreed. We don't need real-time for the initial release."
[seg_000212] Speaker 1: "Let's go with post-meeting then. We can always add real-time later if users request it."`,
		ExpectedOutput: `{
  "decisions": [
    {
      "text": "Use post-meeting diarization for V1, with real-time as a potential future enhancement.",
      "speaker_ids": ["spk_0", "spk_1"],
      "evidence": [
        {"segment_id": "seg_000210", "quote": "post-meeting diarization is the right approach for V1"},
        {"segment_id": "seg_000211", "quote": "We don't need real-time for the initial release"}
      ]
    }
  ]
}`,
	},
	{
		Name: "Technology Stack Decision",
		Transcript: `[seg_000320] Speaker 2: "We should consider SQLite for the local search index."
[seg_000321] Speaker 1: "SQLite with FTS5 would give us full-text search without adding complexity."
[seg_000322] Speaker 2: "FTS5 BM25 ranking is exactly what we need for keyword search."
[seg_000323] Speaker 0: "Sounds good. Let's use SQLite FTS5 for the search index."`,
		ExpectedOutput: `{
  "decisions": [
    {
      "text": "Use SQLite with FTS5 for the local search index, using BM25 ranking for keyword search.",
      "speaker_ids": ["spk_0", "spk_1", "spk_2"],
      "evidence": [
        {"segment_id": "seg_000321", "quote": "SQLite with FTS5 would give us full-text search without adding complexity"},
        {"segment_id": "seg_000322", "quote": "FTS5 BM25 ranking is exactly what we need for keyword search"}
      ]
    }
  ]
}`,
	},
	{
		Name: "Vendor Selection Decision",
		Transcript: `[seg_000450] Speaker 0: "We've tested both AssemblyAI and ElevenLabs for transcription."
[seg_000451] Speaker 1: "AssemblyAI gave us better diarization results in our benchmarks."
[seg_000452] Speaker 0: "The cost difference is significant too. AssemblyAI is more affordable."
[seg_000453] Speaker 1: "Let's go with AssemblyAI as our default provider."
[seg_000454] Speaker 0: "Agreed. We can always benchmark others later if needed."`,
		ExpectedOutput: `{
  "decisions": [
    {
      "text": "Use AssemblyAI as the default STT provider based on benchmark diarization quality and cost.",
      "speaker_ids": ["spk_0", "spk_1"],
      "evidence": [
        {"segment_id": "seg_000451", "quote": "AssemblyAI gave us better diarization results in our benchmarks"},
        {"segment_id": "seg_000452", "quote": "The cost difference is significant too. AssemblyAI is more affordable"}
      ]
    }
  ]
}`,
	},
}

type DecisionExample struct {
	Name           string
	Transcript     string
	ExpectedOutput string
}

var FewShotRiskExamples = []RiskExample{
	{
		Name: "Performance Risk",
		Transcript: `[seg_000600] Speaker 1: "The main concern I have is that local transcription might exceed our V1 latency targets."
[seg_000601] Speaker 0: "We've been seeing 2-3x realtime for local Whisper models."
[seg_000602] Speaker 1: "For a 30-minute meeting, that's potentially 60-90 minutes of processing time."
[seg_000603] Speaker 0: "That's definitely a problem if users are expecting near-instant results."`,
		ExpectedOutput: `{
  "risks": [
    {
      "text": "Local transcription may exceed V1 latency targets - Whisper models running 2-3x realtime could mean 60-90 minutes for 30-minute meetings.",
      "speaker_ids": ["spk_0", "spk_1"],
      "evidence": [
        {"segment_id": "seg_000600", "quote": "local transcription might exceed our V1 latency targets"},
        {"segment_id": "seg_000601", "quote": "We've been seeing 2-3x realtime for local Whisper models"}
      ]
    }
  ]
}`,
	},
	{
		Name: "Dependency Risk",
		Transcript: `[seg_000700] Speaker 0: "The feature depends on the new API endpoint that the platform team is building."
[seg_000701] Speaker 1: "What's the timeline on that?"
[seg_000702] Speaker 0: "They said Q3, but it's not committed yet."
[seg_000703] Speaker 1: "So we're exposed if that slips."`,
		ExpectedOutput: `{
  "risks": [
    {
      "text": "Feature depends on platform team API endpoint with an uncommitted Q3 timeline - at risk if the dependency slips.",
      "speaker_ids": ["spk_0", "spk_1"],
      "evidence": [
        {"segment_id": "seg_000700", "quote": "The feature depends on the new API endpoint that the platform team is building"},
        {"segment_id": "seg_000702", "quote": "They said Q3, but it's not committed yet"}
      ]
    }
  ]
}`,
	},
}

type RiskExample struct {
	Name           string
	Transcript     string
	ExpectedOutput string
}

func DecisionExamplesString() string {
	var sb strings.Builder
	for i, ex := range FewShotDecisionExamples {
		sb.WriteString(fmt.Sprintf("### Example %d: %s\n", i+1, ex.Name))
		sb.WriteString("Transcript excerpt:\n")
		sb.WriteString(ex.Transcript)
		sb.WriteString("\n\nOutput:\n")
		sb.WriteString(ex.ExpectedOutput)
		sb.WriteString("\n\n")
	}
	return sb.String()
}

func RiskExamplesString() string {
	var sb strings.Builder
	for i, ex := range FewShotRiskExamples {
		sb.WriteString(fmt.Sprintf("### Example %d: %s\n", i+1, ex.Name))
		sb.WriteString("Transcript excerpt:\n")
		sb.WriteString(ex.Transcript)
		sb.WriteString("\n\nOutput:\n")
		sb.WriteString(ex.ExpectedOutput)
		sb.WriteString("\n\n")
	}
	return sb.String()
}
