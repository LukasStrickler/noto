// Package data provides repository interfaces and SQLite implementations for
// persistent speaker profiles and meeting-speaker mappings.
package speakerstore

import "time"

// Affiliation is one context a person belongs to — e.g. a university and a
// project are two affiliations, each with their own organization and email.
type Affiliation struct {
	Context      string `json:"context"`      // free label, e.g. "University", "Project X"
	Organization string `json:"organization"` // org / institution name
	Email        string `json:"email"`        // contact email for this context
}

// SpeakerProfile represents a persistent speaker profile with voice embedding.
type SpeakerProfile struct {
	ID              string // UUID
	DisplayName     string
	Email           string // legacy/primary email; affiliations carry per-context ones
	Pronouns        string
	Notes           string
	Affiliations    []Affiliation
	EmbeddingVector []float64 // 192-dim
	EmbeddingDim    int
	// EmbeddingCount is how many enrollment observations the centroid summarizes,
	// so auto-match updates fold in a new voiceprint as a true running mean
	// (weight 1/(count+1)) instead of a 50/50 average. >=1 for any enrolled
	// profile; 0/absent is treated as 1.
	EmbeddingCount int
	EmbeddingModel string // e.g. "ecapa"
	CreatedAt      time.Time
	UpdatedAt      time.Time
	LastSeenAt     *time.Time
}

// MeetingSpeakerMapping links a speaker in a specific meeting to a profile.
type MeetingSpeakerMapping struct {
	MeetingID        string
	MeetingSpeakerID string  // diarizer speaker label like "A", "B"
	ProviderLabel    string  // Original from provider
	ProfileID        *string // nil if unmatched
	MatchConfidence  *float64
	MatchStatus      string // "auto", "pending", "manual", "new", "unmatched"
	// EmbeddingVector is this meeting-speaker's voiceprint, persisted so
	// suggestions can be (re-)ranked against the profile library without
	// re-decoding the audio. Nil when no embedder ran.
	EmbeddingVector []float64
	EmbeddingDim    int
	CreatedAt       time.Time
	UpdatedAt       time.Time
}
