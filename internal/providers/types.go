package providers

import "sort"

type Capability string

const (
	CapabilityTranscribe       Capability = "transcribe"
	CapabilityDiarize          Capability = "diarize"
	CapabilityWordTimestamps   Capability = "word_timestamps"
	CapabilityAudioTags        Capability = "audio_tags"
	CapabilityContextBiasing   Capability = "context_biasing"
	CapabilitySummarize        Capability = "summarize"
	CapabilityExtractActions   Capability = "extract_actions"
	CapabilityExtractDecisions Capability = "extract_decisions"
	CapabilityExtractRisks     Capability = "extract_risks"
	CapabilityClassify         Capability = "classify"
	CapabilityChat             Capability = "chat"
)

var LLMCapabilities = map[Capability]bool{
	CapabilitySummarize:        true,
	CapabilityExtractActions:   true,
	CapabilityExtractDecisions: true,
	CapabilityExtractRisks:     true,
	CapabilityClassify:         true,
	CapabilityChat:             true,
}

type ProviderKind string

const (
	ProviderKindSpeech ProviderKind = "speech"
	ProviderKindLLM    ProviderKind = "llm"
	ProviderKindFake   ProviderKind = "fake"
)

type ProviderSuite struct {
	ID                     string       `json:"id"`
	DisplayName            string       `json:"display_name"`
	Kind                   ProviderKind `json:"kind"`
	Capabilities           []Capability `json:"capabilities"`
	Models                 []Model      `json:"models"`
	CredentialRef          string       `json:"credential_ref"`
	RequiresNetwork        bool         `json:"requires_network"`
	SendsRawAudioOffDevice bool         `json:"sends_raw_audio_off_device"`
	PricingHint            string       `json:"pricing_hint"`
	Notes                  string       `json:"notes"`
}

type Model struct {
	ID           string       `json:"id"`
	DisplayName  string       `json:"display_name"`
	Capabilities []Capability `json:"capabilities"`
	PricingHint  string       `json:"pricing_hint,omitempty"`
}

func (p ProviderSuite) HasCapability(cap Capability) bool {
	for _, c := range p.Capabilities {
		if c == cap {
			return true
		}
	}
	return false
}

func SortedCapabilities(caps []Capability) []Capability {
	out := append([]Capability(nil), caps...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
