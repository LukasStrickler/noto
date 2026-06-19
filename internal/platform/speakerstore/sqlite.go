package speakerstore

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/lukasstrickler/noto/internal/platform/db"
)

// Migrate applies the speaker-store schema to an already-open handle. The
// speaker profile + meeting-mapping tables live in noto.sqlite alongside the
// search index, so the composition root opens that file once (db.Open) and
// applies this schema on the shared handle. After the base CREATE TABLEs it
// runs additive column migrations so databases created before these columns
// existed gain them in place.
func Migrate(d *db.DB) error {
	if err := d.Migrate(speakerSchema); err != nil {
		return err
	}
	return migrateAddColumns(d)
}

// migrateAddColumns adds columns introduced after the initial schema. db.Migrate
// is not safe to re-run with bare ALTER TABLE (a second run errors on the
// already-present column), so each add is guarded by a PRAGMA table_info check.
func migrateAddColumns(d *db.DB) error {
	adds := []struct{ table, column, ddl string }{
		{"speaker_profiles", "notes", "ALTER TABLE speaker_profiles ADD COLUMN notes TEXT NOT NULL DEFAULT ''"},
		{"speaker_profiles", "affiliations", "ALTER TABLE speaker_profiles ADD COLUMN affiliations TEXT NOT NULL DEFAULT '[]'"},
		{"meeting_speaker_mappings", "embedding_vector", "ALTER TABLE meeting_speaker_mappings ADD COLUMN embedding_vector BLOB"},
		{"meeting_speaker_mappings", "embedding_dim", "ALTER TABLE meeting_speaker_mappings ADD COLUMN embedding_dim INTEGER NOT NULL DEFAULT 0"},
	}
	for _, a := range adds {
		has, err := columnExists(d, a.table, a.column)
		if err != nil {
			return err
		}
		if has {
			continue
		}
		if _, err := d.Exec(a.ddl); err != nil {
			return fmt.Errorf("speakerstore add column %s.%s: %w", a.table, a.column, err)
		}
	}
	return nil
}

func columnExists(d *db.DB, table, column string) (bool, error) {
	rows, err := d.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt any
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
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
		notes TEXT NOT NULL DEFAULT '',
		affiliations TEXT NOT NULL DEFAULT '[]',
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
		embedding_vector BLOB,
		embedding_dim INTEGER NOT NULL DEFAULT 0,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL,
		PRIMARY KEY (meeting_id, meeting_speaker_id)
	)`,
	`CREATE INDEX IF NOT EXISTS idx_meeting_speaker_mappings_profile ON meeting_speaker_mappings(profile_id)`,
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
		`INSERT INTO speaker_profiles (id, display_name, email, pronouns, notes, affiliations, embedding_vector, embedding_dim, embedding_model, created_at, updated_at, last_seen_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.DisplayName, p.Email, p.Pronouns, p.Notes, marshalAffiliations(p.Affiliations), vec, p.EmbeddingDim, p.EmbeddingModel,
		p.CreatedAt.Unix(), p.UpdatedAt.Unix(), toUnixTime(p.LastSeenAt))
	return err
}

const profileColumns = `id, display_name, email, pronouns, notes, affiliations, embedding_vector, embedding_dim, embedding_model, created_at, updated_at, last_seen_at`

func (r *sqliteSpeakerProfileRepo) Get(ctx context.Context, id string) (SpeakerProfile, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT `+profileColumns+` FROM speaker_profiles WHERE id = ?`, id)
	return scanSpeakerProfile(row)
}

func (r *sqliteSpeakerProfileRepo) List(ctx context.Context) ([]SpeakerProfile, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+profileColumns+` FROM speaker_profiles ORDER BY created_at DESC`)
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
		`UPDATE speaker_profiles SET display_name=?, email=?, pronouns=?, notes=?, affiliations=?, embedding_vector=?, embedding_dim=?, embedding_model=?, updated_at=?, last_seen_at=?
		 WHERE id=?`,
		p.DisplayName, p.Email, p.Pronouns, p.Notes, marshalAffiliations(p.Affiliations), vec, p.EmbeddingDim, p.EmbeddingModel, p.UpdatedAt.Unix(), toUnixTime(p.LastSeenAt), p.ID)
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

const mappingColumns = `meeting_id, meeting_speaker_id, provider_label, profile_id, match_confidence, match_status, embedding_vector, embedding_dim, created_at, updated_at`

func (r *sqliteMeetingSpeakerMappingRepo) Upsert(ctx context.Context, m MeetingSpeakerMapping) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO meeting_speaker_mappings (meeting_id, meeting_speaker_id, provider_label, profile_id, match_confidence, match_status, embedding_vector, embedding_dim, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(meeting_id, meeting_speaker_id) DO UPDATE SET
			provider_label=excluded.provider_label, profile_id=excluded.profile_id,
			match_confidence=excluded.match_confidence, match_status=excluded.match_status,
			embedding_vector=excluded.embedding_vector, embedding_dim=excluded.embedding_dim,
			updated_at=excluded.updated_at`,
		m.MeetingID, m.MeetingSpeakerID, m.ProviderLabel, m.ProfileID, m.MatchConfidence, m.MatchStatus,
		marshalEmbedding(m.EmbeddingVector), m.EmbeddingDim, m.CreatedAt.Unix(), m.UpdatedAt.Unix())
	return err
}

func (r *sqliteMeetingSpeakerMappingRepo) ListByMeeting(ctx context.Context, meetingID string) ([]MeetingSpeakerMapping, error) {
	return r.queryMappings(ctx,
		`SELECT `+mappingColumns+` FROM meeting_speaker_mappings WHERE meeting_id = ? ORDER BY meeting_speaker_id`, meetingID)
}

func (r *sqliteMeetingSpeakerMappingRepo) ListByProfile(ctx context.Context, profileID string) ([]MeetingSpeakerMapping, error) {
	return r.queryMappings(ctx,
		`SELECT `+mappingColumns+` FROM meeting_speaker_mappings WHERE profile_id = ? ORDER BY updated_at DESC`, profileID)
}

func (r *sqliteMeetingSpeakerMappingRepo) ReassignProfile(ctx context.Context, oldID, newID string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE meeting_speaker_mappings SET profile_id=?, updated_at=? WHERE profile_id=?`,
		newID, time.Now().Unix(), oldID)
	return err
}

func (r *sqliteMeetingSpeakerMappingRepo) queryMappings(ctx context.Context, query string, args ...any) ([]MeetingSpeakerMapping, error) {
	rows, err := r.db.QueryContext(ctx, query, args...)
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

func (r *sqliteMeetingSpeakerMappingRepo) CountUnresolved(ctx context.Context) (int, error) {
	var n int
	err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM meeting_speaker_mappings WHERE match_status NOT IN ('auto','manual')`).Scan(&n)
	return n, err
}

func (r *sqliteMeetingSpeakerMappingRepo) StatusCountsByMeeting(ctx context.Context) (map[string]map[string]int, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT meeting_id, match_status, COUNT(*) FROM meeting_speaker_mappings GROUP BY meeting_id, match_status`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]map[string]int{}
	for rows.Next() {
		var meetingID, status string
		var count int
		if err := rows.Scan(&meetingID, &status, &count); err != nil {
			return nil, err
		}
		if out[meetingID] == nil {
			out[meetingID] = map[string]int{}
		}
		out[meetingID][status] = count
	}
	return out, rows.Err()
}

func scanSpeakerProfile(scanner interface{ Scan(...interface{}) error }) (SpeakerProfile, error) {
	var p SpeakerProfile
	var vec []byte
	var affiliations string
	var lastSeenUnix *int64
	var createdUnix, updatedUnix int64
	err := scanner.Scan(&p.ID, &p.DisplayName, &p.Email, &p.Pronouns, &p.Notes, &affiliations, &vec, &p.EmbeddingDim, &p.EmbeddingModel,
		&createdUnix, &updatedUnix, &lastSeenUnix)
	if err != nil {
		return SpeakerProfile{}, err
	}
	p.CreatedAt = time.Unix(createdUnix, 0)
	p.UpdatedAt = time.Unix(updatedUnix, 0)
	p.EmbeddingVector = unmarshalEmbedding(vec)
	p.Affiliations = unmarshalAffiliations(affiliations)
	p.LastSeenAt = fromUnixTime(lastSeenUnix)
	return p, nil
}

func scanMeetingSpeakerMapping(scanner interface{ Scan(...interface{}) error }) (MeetingSpeakerMapping, error) {
	var m MeetingSpeakerMapping
	var vec []byte
	var createdUnix, updatedUnix int64
	err := scanner.Scan(&m.MeetingID, &m.MeetingSpeakerID, &m.ProviderLabel, &m.ProfileID, &m.MatchConfidence, &m.MatchStatus,
		&vec, &m.EmbeddingDim, &createdUnix, &updatedUnix)
	if err != nil {
		return MeetingSpeakerMapping{}, err
	}
	m.EmbeddingVector = unmarshalEmbedding(vec)
	m.CreatedAt = time.Unix(createdUnix, 0)
	m.UpdatedAt = time.Unix(updatedUnix, 0)
	return m, nil
}

func marshalAffiliations(a []Affiliation) string {
	if len(a) == 0 {
		return "[]"
	}
	b, err := json.Marshal(a)
	if err != nil {
		return "[]"
	}
	return string(b)
}

func unmarshalAffiliations(s string) []Affiliation {
	if s == "" || s == "[]" {
		return nil
	}
	var out []Affiliation
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil
	}
	return out
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
