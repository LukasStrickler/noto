// Package data provides repository interfaces and SQLite implementations for
// persistent speaker profiles and meeting-speaker mappings.
package speakerstore

import "time"

// SpeakerProfile represents a persistent speaker profile with voice embedding.
type SpeakerProfile struct {
	ID              string // UUID
	DisplayName     string
	Email           string
	Pronouns        string
	EmbeddingVector []float64 // 192-dim
	EmbeddingDim    int
	EmbeddingModel  string // "titanet-large" or "pyannote"
	CreatedAt       time.Time
	UpdatedAt       time.Time
	LastSeenAt      *time.Time
}

// MeetingSpeakerMapping links a speaker in a specific meeting to a profile.
type MeetingSpeakerMapping struct {
	MeetingID        string
	MeetingSpeakerID string  // AssemblyAI label like "A", "B"
	ProviderLabel    string  // Original from provider
	ProfileID        *string // nil if unmatched
	MatchConfidence  *float64
	MatchStatus      string // "auto", "pending", "manual"
	CreatedAt        time.Time
	UpdatedAt        time.Time
}
