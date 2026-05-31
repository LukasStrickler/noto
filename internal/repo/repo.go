// Package repo defines the ArtifactRepository interface that abstracts
// meeting artifact persistence. The LocalArtifactRepository implementation
// uses the local filesystem + SQLite. A future backend (Postgres + S3) can
// satisfy the same interface without changing any service code.
package repo

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/lukasstrickler/noto/internal/artifacts"
)

// ErrNotFound is returned by repository methods when the requested
// meeting or artifact does not exist.
var ErrNotFound = errors.New("meeting not found")

// ArtifactRepository is the single seam between the service layer and
// any storage backend. Implementations must be safe for concurrent use.
type ArtifactRepository interface {
	// --- Meeting lifecycle ---

	// CreateMeeting initialises storage for a new meeting (creates dirs,
	// writes an initial manifest). Call PrepareAudio first if you need
	// the destination path for an audio file.
	CreateMeeting(ctx context.Context, id uuid.UUID, opts CreateMeetingOpts) error

	// GetMeeting returns the enriched meeting record (counts, status).
	GetMeeting(ctx context.Context, id uuid.UUID) (*StoredMeeting, error)

	// ListMeetings returns all meetings, most-recent first, each
	// pre-enriched with counts so callers don't need a second round-trip.
	ListMeetings(ctx context.Context) ([]*StoredMeeting, error)

	// CountMeetings returns the number of stored meetings without reading
	// or enriching any artifacts — cheap enough to call on every status
	// update.
	CountMeetings(ctx context.Context) (int, error)

	DeleteMeeting(ctx context.Context, id uuid.UUID) error

	// --- Artifact I/O ---

	SaveTranscript(ctx context.Context, id uuid.UUID, t *artifacts.Transcript) error
	LoadTranscript(ctx context.Context, id uuid.UUID) (*artifacts.Transcript, error)

	// SaveSummary persists the rendered markdown and the structured
	// summary. summary may be nil when only the markdown is available.
	SaveSummary(ctx context.Context, id uuid.UUID, md string, summary *artifacts.Summary) error
	LoadSummary(ctx context.Context, id uuid.UUID) (string, *artifacts.Summary, error)

	// --- Audio ---

	// PrepareAudio ensures the meeting storage directory exists and
	// returns the path where an audio file should be written.
	// ext must include the leading dot (e.g. ".m4a"); pass "" to use
	// the repository's default extension.
	PrepareAudio(ctx context.Context, id uuid.UUID, ext string) (path string, err error)

	// AudioPath returns the stored audio path and whether it exists.
	AudioPath(id uuid.UUID) (string, bool)

	// --- Integrity ---

	// VerifyIntegrity checks stored checksums for the meeting's artifacts.
	VerifyIntegrity(ctx context.Context, id uuid.UUID) error

	// --- Filesystem paths (agent/CLI use) ---

	// FilePaths returns on-disk artifact paths. Returns nil for backends
	// that do not expose a local filesystem (e.g. Postgres + S3).
	FilePaths(id uuid.UUID) *MeetingFilePaths
}

// StoredMeeting is the repository's enriched view of a meeting.
// Counts and status flags are derived from artifact data so that
// the service layer does not need extra round-trips.
type StoredMeeting struct {
	ID               uuid.UUID
	Title            string
	CurrentVersionID string
	CreatedAt        time.Time
	Versions         []StoredVersion

	// Status helpers.
	HasTranscript bool
	HasSummary    bool

	// Derived from transcript.
	DurationSeconds int
	SpeakerCount    int

	// Derived from summary.
	ShortSummary  string
	DecisionCount int
	ActionCount   int
	RiskCount     int
	QuestionCount int
}

// StoredVersion mirrors a single manifest version entry.
type StoredVersion struct {
	ID        string
	CreatedAt time.Time
	Reason    string
	Checksum  string
}

// CreateMeetingOpts carries metadata for a new meeting.
type CreateMeetingOpts struct {
	Title     string
	Reason    string    // e.g. "audio_imported", "recorded"
	CreatedAt time.Time // if zero, defaults to time.Now()
}

// MeetingFilePaths is the on-disk artifact path bundle returned by
// LocalArtifactRepository. Non-filesystem backends return nil.
type MeetingFilePaths struct {
	MeetingDir  string
	Manifest    string
	Transcript  string
	SummaryMD   string
	SummaryJSON string
	Audio       string
}
