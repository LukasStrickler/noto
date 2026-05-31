package speakerstore

import (
	"context"
	"encoding/binary"
	"math"
	"time"

	"github.com/lukasstrickler/noto/internal/platform/db"
)

// Migrate applies the speaker-store schema to an already-open handle. The
// speaker profile + meeting-mapping tables live in noto.sqlite alongside the
// search index, so the composition root opens that file once (db.Open) and
// applies this schema on the shared handle.
func Migrate(d *db.DB) error {
	return d.Migrate(speakerSchema)
}

// Open opens noto's SQLite database and applies the speaker-store schema. Most
// callers should open once via db.Open and pass the single handle to both the
// search index and these repositories (see internal/notohost); this helper is
// for standalone and test use.
func Open(path string) (*db.DB, error) {
	d, err := db.Open(path)
	if err != nil {
		return nil, err
	}
	if err := Migrate(d); err != nil {
		_ = d.Close()
		return nil, err
	}
	return d, nil
}

var speakerSchema = []string{
	`CREATE TABLE IF NOT EXISTS speaker_profiles (
		id TEXT PRIMARY KEY,
		display_name TEXT NOT NULL,
		email TEXT NOT NULL DEFAULT '',
		pronouns TEXT NOT NULL DEFAULT '',
		embedding_vector BLOB,
		embedding_dim INTEGER NOT NULL DEFAULT 0,
		embedding_model TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL,
		last_seen_at INTEGER
	)`,
	`CREATE TABLE IF NOT EXISTS meeting_speaker_mappings (
		meeting_id TEXT NOT NULL,
		meeting_speaker_id TEXT NOT NULL,
		provider_label TEXT NOT NULL DEFAULT '',
		profile_id TEXT,
		match_confidence REAL,
		match_status TEXT NOT NULL DEFAULT 'pending',
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL,
		PRIMARY KEY (meeting_id, meeting_speaker_id)
	)`,
}

type sqliteSpeakerProfileRepo struct {
	db *db.DB
}

func NewSQLiteSpeakerProfileRepository(d *db.DB) SpeakerProfileRepository {
	return &sqliteSpeakerProfileRepo{db: d}
}

func (r *sqliteSpeakerProfileRepo) Create(ctx context.Context, p SpeakerProfile) error {
	vec := marshalEmbedding(p.EmbeddingVector)
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO speaker_profiles (id, display_name, email, pronouns, embedding_vector, embedding_dim, embedding_model, created_at, updated_at, last_seen_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.DisplayName, p.Email, p.Pronouns, vec, p.EmbeddingDim, p.EmbeddingModel,
		p.CreatedAt.Unix(), p.UpdatedAt.Unix(), toUnixTime(p.LastSeenAt))
	return err
}

func (r *sqliteSpeakerProfileRepo) Get(ctx context.Context, id string) (SpeakerProfile, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT id, display_name, email, pronouns, embedding_vector, embedding_dim, embedding_model, created_at, updated_at, last_seen_at
		 FROM speaker_profiles WHERE id = ?`, id)
	return scanSpeakerProfile(row)
}

func (r *sqliteSpeakerProfileRepo) List(ctx context.Context) ([]SpeakerProfile, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, display_name, email, pronouns, embedding_vector, embedding_dim, embedding_model, created_at, updated_at, last_seen_at
		 FROM speaker_profiles ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var profiles []SpeakerProfile
	for rows.Next() {
		p, err := scanSpeakerProfile(rows)
		if err != nil {
			return nil, err
		}
		profiles = append(profiles, p)
	}
	return profiles, rows.Err()
}

func (r *sqliteSpeakerProfileRepo) Update(ctx context.Context, p SpeakerProfile) error {
	vec := marshalEmbedding(p.EmbeddingVector)
	_, err := r.db.ExecContext(ctx,
		`UPDATE speaker_profiles SET display_name=?, email=?, pronouns=?, embedding_vector=?, embedding_dim=?, embedding_model=?, updated_at=?, last_seen_at=?
		 WHERE id=?`,
		p.DisplayName, p.Email, p.Pronouns, vec, p.EmbeddingDim, p.EmbeddingModel, p.UpdatedAt.Unix(), toUnixTime(p.LastSeenAt), p.ID)
	return err
}

func (r *sqliteSpeakerProfileRepo) Delete(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM speaker_profiles WHERE id=?`, id)
	return err
}

type sqliteMeetingSpeakerMappingRepo struct {
	db *db.DB
}

func NewSQLiteMeetingSpeakerMappingRepository(d *db.DB) MeetingSpeakerMappingRepository {
	return &sqliteMeetingSpeakerMappingRepo{db: d}
}

func (r *sqliteMeetingSpeakerMappingRepo) Upsert(ctx context.Context, m MeetingSpeakerMapping) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO meeting_speaker_mappings (meeting_id, meeting_speaker_id, provider_label, profile_id, match_confidence, match_status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(meeting_id, meeting_speaker_id) DO UPDATE SET
			provider_label=excluded.provider_label, profile_id=excluded.profile_id,
			match_confidence=excluded.match_confidence, match_status=excluded.match_status, updated_at=excluded.updated_at`,
		m.MeetingID, m.MeetingSpeakerID, m.ProviderLabel, m.ProfileID, m.MatchConfidence, m.MatchStatus, m.CreatedAt.Unix(), m.UpdatedAt.Unix())
	return err
}

func (r *sqliteMeetingSpeakerMappingRepo) ListByMeeting(ctx context.Context, meetingID string) ([]MeetingSpeakerMapping, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT meeting_id, meeting_speaker_id, provider_label, profile_id, match_confidence, match_status, created_at, updated_at
		 FROM meeting_speaker_mappings WHERE meeting_id = ? ORDER BY meeting_speaker_id`, meetingID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var mappings []MeetingSpeakerMapping
	for rows.Next() {
		m, err := scanMeetingSpeakerMapping(rows)
		if err != nil {
			return nil, err
		}
		mappings = append(mappings, m)
	}
	return mappings, rows.Err()
}

func (r *sqliteMeetingSpeakerMappingRepo) DeleteByMeeting(ctx context.Context, meetingID string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM meeting_speaker_mappings WHERE meeting_id=?`, meetingID)
	return err
}

func scanSpeakerProfile(scanner interface{ Scan(...interface{}) error }) (SpeakerProfile, error) {
	var p SpeakerProfile
	var vec []byte
	var lastSeenUnix *int64
	var createdUnix, updatedUnix int64
	err := scanner.Scan(&p.ID, &p.DisplayName, &p.Email, &p.Pronouns, &vec, &p.EmbeddingDim, &p.EmbeddingModel,
		&createdUnix, &updatedUnix, &lastSeenUnix)
	if err != nil {
		return SpeakerProfile{}, err
	}
	p.CreatedAt = time.Unix(createdUnix, 0)
	p.UpdatedAt = time.Unix(updatedUnix, 0)
	p.EmbeddingVector = unmarshalEmbedding(vec)
	p.LastSeenAt = fromUnixTime(lastSeenUnix)
	return p, nil
}

func scanMeetingSpeakerMapping(scanner interface{ Scan(...interface{}) error }) (MeetingSpeakerMapping, error) {
	var m MeetingSpeakerMapping
	var createdUnix, updatedUnix int64
	err := scanner.Scan(&m.MeetingID, &m.MeetingSpeakerID, &m.ProviderLabel, &m.ProfileID, &m.MatchConfidence, &m.MatchStatus,
		&createdUnix, &updatedUnix)
	if err != nil {
		return MeetingSpeakerMapping{}, err
	}
	m.CreatedAt = time.Unix(createdUnix, 0)
	m.UpdatedAt = time.Unix(updatedUnix, 0)
	return m, nil
}

func marshalEmbedding(vec []float64) []byte {
	if vec == nil {
		return nil
	}
	buf := make([]byte, len(vec)*8)
	for i, v := range vec {
		binary.LittleEndian.PutUint64(buf[i*8:], math.Float64bits(v))
	}
	return buf
}

func unmarshalEmbedding(data []byte) []float64 {
	if len(data) == 0 {
		return nil
	}
	n := len(data) / 8
	vec := make([]float64, n)
	for i := 0; i < n; i++ {
		vec[i] = math.Float64frombits(binary.LittleEndian.Uint64(data[i*8:]))
	}
	return vec
}

func toUnixTime(t *time.Time) *int64 {
	if t == nil {
		return nil
	}
	v := t.Unix()
	return &v
}

func fromUnixTime(v *int64) *time.Time {
	if v == nil {
		return nil
	}
	t := time.Unix(*v, 0)
	return &t
}
