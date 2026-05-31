package search

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/lukasstrickler/noto/internal/platform/db"
)

type SearchIndex struct {
	db   *sql.DB
	mu   sync.RWMutex
	path string
	// ownsConn is true when this index opened its own connection (and must
	// close it); false when the connection is borrowed from a caller that
	// shares it with other components.
	ownsConn bool
}

type SearchResult struct {
	MeetingID   string
	Title       string
	Snippet     string
	Speaker     string
	SegmentText string
	BM25Score   float64
	SegmentID   string
	Timestamp   float64
	ResultType  ResultType
	CreatedAt   time.Time
}

// MeetingHits aggregates per-meeting search counts for the UI: titles
// first, then meetings with transcript/summary text matches, ordered by
// best score and recency tiebreaker.
type MeetingHits struct {
	MeetingID       string
	MeetingTitle    string
	CreatedAt       time.Time
	TitleMatch      bool
	SummaryMatch    bool // body of the short summary matched
	TranscriptCount int
	SummaryCount    int // structured summary bullets (decisions+actions+risks+questions)
	QuestionCount   int
	BestScore       float64
	Snippet         string
	Hits            []SearchResult
}

type ResultType string

const (
	ResultTypeTitle      ResultType = "title"
	ResultTypeTranscript ResultType = "transcript"
	ResultTypeDecision   ResultType = "decision"
	ResultTypeAction     ResultType = "action"
	ResultTypeRisk       ResultType = "risk"
	ResultTypeQuestion   ResultType = "question"
	ResultTypeSummary    ResultType = "summary"
)

// FTS5 column order for bm25() weights. KEEP IN SYNC with the
// CREATE VIRTUAL TABLE statement in NewSearchIndex AND with
// schemaVersion when columns change.
const (
	colContent     = 0
	colMeetingID   = 1
	colTitle       = 2
	colSegmentText = 3
	colSpeaker     = 4
	colDecisions   = 5
	colActions     = 6
	colRisks       = 7
	colQuestions   = 8
	colSummaryBody = 9
	colSegmentID   = 10
	colResultType  = 11
	colCount       = 12
)

// schemaVersion is bumped any time the FTS5 column set changes. On
// open we drop and recreate the index if the on-disk version doesn't
// match — the data is re-derivable by re-indexing meetings.
const schemaVersion = 3

// Per-column BM25 weights. Title is heavily boosted so a title-match
// always outranks a body-only match. The summary body sits between
// title and structured-summary bullets so a generic "this meeting is
// about X" hit beats a same-token transcript hit. ID columns get 0 —
// they're stored for filtering, not relevance.
var columnWeights = [colCount]float64{
	colContent:     1.0,
	colMeetingID:   0.0,
	colTitle:       5.0,
	colSegmentText: 1.0,
	colSpeaker:     0.5,
	colDecisions:   2.0,
	colActions:     2.0,
	colRisks:       2.0,
	colQuestions:   2.0,
	colSummaryBody: 3.0,
	colSegmentID:   0.0,
	colResultType:  0.0,
}

// bm25WeightsCSV is the comma-separated bm25() column-weight list. The weights
// are constant, so it is computed once here rather than on every Search call.
var bm25WeightsCSV = func() string {
	parts := make([]string, 0, colCount)
	for _, w := range columnWeights {
		parts = append(parts, fmt.Sprintf("%.2f", w))
	}
	return strings.Join(parts, ", ")
}()

func (s *SearchIndex) IndexMeeting(meeting *Meeting) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM meetings_fts WHERE meeting_id = ?`, meeting.MeetingID); err != nil {
		return fmt.Errorf("delete existing entries: %w", err)
	}

	insert := func(e entryRow) error {
		e.meetingID = meeting.MeetingID
		return s.insertEntry(tx, e)
	}

	if meeting.Title != "" {
		if err := insert(entryRow{title: meeting.Title, resultType: "title"}); err != nil {
			return err
		}
	}

	// summary_body holds the full short summary as one row so a hit
	// anywhere in the summary text outranks transcript-only hits.
	if meeting.ShortSummary != "" {
		if err := insert(entryRow{summaryBody: meeting.ShortSummary, resultType: "summary"}); err != nil {
			return err
		}
	}

	for _, seg := range meeting.TranscriptSegments {
		if seg.Text != "" {
			if err := insert(entryRow{
				segmentText: seg.Text,
				speaker:     seg.Speaker,
				segmentID:   seg.SegmentID,
				resultType:  "transcript",
			}); err != nil {
				return err
			}
		}
	}

	for _, dec := range meeting.Decisions {
		if dec.Text != "" {
			if err := insert(entryRow{decisions: dec.Text, resultType: "decision"}); err != nil {
				return err
			}
		}
	}

	for _, act := range meeting.ActionItems {
		if act.Text != "" {
			text := strings.TrimSpace(act.Text + " " + act.Owner)
			if err := insert(entryRow{actions: text, resultType: "action"}); err != nil {
				return err
			}
		}
	}

	for _, risk := range meeting.Risks {
		if risk.Text != "" {
			if err := insert(entryRow{risks: risk.Text, resultType: "risk"}); err != nil {
				return err
			}
		}
	}

	for _, q := range meeting.OpenQuestions {
		if q.Text != "" {
			if err := insert(entryRow{questions: q.Text, resultType: "question"}); err != nil {
				return err
			}
		}
	}

	// Sidecar metadata: title + created_at, used for recency tiebreaker
	// and for surfacing a friendly title even when only a transcript
	// segment matched the query.
	createdAt := meeting.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	if _, err := tx.Exec(`
		INSERT INTO meetings_meta(meeting_id, title, created_at)
		VALUES(?, ?, ?)
		ON CONFLICT(meeting_id) DO UPDATE SET title=excluded.title, created_at=excluded.created_at
	`, meeting.MeetingID, meeting.Title, createdAt.UnixNano()); err != nil {
		return fmt.Errorf("upsert meta: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}

	return nil
}

// Optimize runs SQLite's PRAGMA optimize. It is meant to be called once after
// a batch of IndexMeeting calls (e.g. a full reindex or seed) rather than per
// meeting, which would turn a batch index into N separate optimize passes.
func (s *SearchIndex) Optimize() error {
	if _, err := s.db.Exec(`PRAGMA optimize`); err != nil {
		return fmt.Errorf("optimize index: %w", err)
	}
	return nil
}

// entryRow gathers the per-column values for a single FTS5 row. Each
// IndexMeeting call writes one row per result type (title, transcript
// segment, decision, action, risk, question, summary). Only the
// columns relevant to that row type are populated; the rest stay
// empty so BM25 doesn't get polluted by stale text.
type entryRow struct {
	meetingID   string
	title       string
	segmentText string
	speaker     string
	decisions   string
	actions     string
	risks       string
	questions   string
	summaryBody string
	segmentID   string
	resultType  string
}

func (s *SearchIndex) insertEntry(tx *sql.Tx, e entryRow) error {
	// title is only populated on the dedicated title row — keeps the
	// title column from getting credit for transcript/decision hits.
	titleCol := ""
	if e.resultType == "title" {
		titleCol = e.title
	}
	content := buildContent(titleCol, e.segmentText, e.speaker, e.decisions, e.actions, e.risks, e.questions, e.summaryBody)
	_, err := tx.Exec(
		`INSERT INTO meetings_fts(content, meeting_id, title, segment_text, speaker, decisions, actions, risks, questions, summary_body, segment_id, result_type) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		content, e.meetingID, titleCol, e.segmentText, e.speaker, e.decisions, e.actions, e.risks, e.questions, e.summaryBody, e.segmentID, e.resultType,
	)
	if err != nil {
		return fmt.Errorf("insert entry: %w", err)
	}
	return nil
}

func buildContent(parts ...string) string {
	out := parts[:0]
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, " ")
}

// sanitizeFTS5Query strips characters FTS5 would interpret as operators
// (`*`, `:`, `(`, `)`, `"`, `-`, `+`, `^`, `.`, etc.) and re-emits the
// remaining words as `("term" OR term*)` groups so that an exact-term
// hit and a prefix-only hit can both match a single query. BM25 ranks
// the exact form higher because the prefix form matches a superset
// of documents but contributes less per match. An empty result means
// the query had no usable tokens and the caller should return zero
// results instead of erroring.
func sanitizeFTS5Query(query string) string {
	var b strings.Builder
	for _, r := range query {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r):
			b.WriteRune(r)
		case r == '_' || r == '-' || r == '\'':
			// Keep hyphens/apostrophes inside words so "billing-bug" or
			// "we're" survive tokenization. unicode61 splits on them
			// anyway, but keeping them avoids glueing adjacent words.
			b.WriteRune(' ')
		default:
			b.WriteRune(' ')
		}
	}
	tokens := strings.Fields(b.String())
	if len(tokens) == 0 {
		return ""
	}
	groups := make([]string, len(tokens))
	for i, t := range tokens {
		// Single-character tokens get prefix-only treatment; FTS5
		// rejects bare `"x"` as a too-short phrase on default tokenizers.
		if len(t) == 1 {
			groups[i] = t + `*`
			continue
		}
		groups[i] = `("` + t + `" OR ` + t + `*)`
	}
	return strings.Join(groups, " ")
}

func (s *SearchIndex) Search(query string) ([]SearchResult, error) {
	raw := strings.TrimSpace(query)
	if raw == "" {
		return nil, fmt.Errorf("query required")
	}
	fts := sanitizeFTS5Query(raw)
	if fts == "" {
		// User typed only punctuation. Not an error — there's just
		// nothing tokenizable to match.
		return nil, nil
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	sqlText := `
		SELECT
			f.meeting_id,
			COALESCE(mm.title, f.title) AS title,
			f.segment_text,
			f.speaker,
			f.decisions,
			f.actions,
			f.risks,
			f.questions,
			f.summary_body,
			f.segment_id,
			f.result_type,
			bm25(meetings_fts, ` + bm25WeightsCSV + `) AS rank,
			snippet(meetings_fts, -1, '**', '**', '...', 16) AS snippet,
			COALESCE(mm.created_at, 0) AS created_ns
		FROM meetings_fts f
		LEFT JOIN meetings_meta mm ON mm.meeting_id = f.meeting_id
		WHERE meetings_fts MATCH ?
		ORDER BY rank ASC, created_ns DESC
		LIMIT 500
	`

	rows, err := s.db.Query(sqlText, fts)
	if err != nil {
		return nil, fmt.Errorf("search query: %w", err)
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var r SearchResult
		var title, segmentText, speaker, decisions, actions, risks, questions, summaryBody, segmentID, resultType, snippet sql.NullString
		var rank sql.NullFloat64
		var createdNS sql.NullInt64

		if err := rows.Scan(&r.MeetingID, &title, &segmentText, &speaker, &decisions, &actions, &risks, &questions, &summaryBody, &segmentID, &resultType, &rank, &snippet, &createdNS); err != nil {
			return nil, fmt.Errorf("scan row: %w", err)
		}

		r.Title = title.String
		r.SegmentText = segmentText.String
		r.Speaker = speaker.String
		r.Snippet = snippet.String
		r.SegmentID = segmentID.String
		r.ResultType = ResultType(resultType.String)
		if rank.Valid {
			r.BM25Score = rank.Float64
		}
		if createdNS.Valid && createdNS.Int64 != 0 {
			r.CreatedAt = time.Unix(0, createdNS.Int64).UTC()
		}

		if r.Snippet == "" {
			switch {
			case title.String != "":
				r.Snippet = title.String
			case segmentText.String != "":
				r.Snippet = segmentText.String
			case summaryBody.String != "":
				r.Snippet = summaryBody.String
			case decisions.String != "":
				r.Snippet = decisions.String
			case actions.String != "":
				r.Snippet = actions.String
			case risks.String != "":
				r.Snippet = risks.String
			case questions.String != "":
				r.Snippet = questions.String
			}
		}

		results = append(results, r)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error: %w", err)
	}

	return results, nil
}

// SearchMeetings groups Search results by meeting and ranks groups so
// that title matches outrank body matches and, among equally relevant
// meetings, the most recent wins. Returns up to `limit` meetings; 0 or
// negative means no cap.
func (s *SearchIndex) SearchMeetings(query string, limit int) ([]MeetingHits, error) {
	hits, err := s.Search(query)
	if err != nil {
		return nil, err
	}
	return s.GroupMeetings(hits, limit), nil
}

// GroupMeetings groups already-fetched Search results by meeting and ranks
// the groups. Callers that need both the flat hit list and the grouped view
// (e.g. Service.Search) can run Search once and pass the results here instead
// of querying the FTS index twice.
func (s *SearchIndex) GroupMeetings(hits []SearchResult, limit int) []MeetingHits {
	if len(hits) == 0 {
		return nil
	}
	groups := map[string]*MeetingHits{}
	order := []string{}
	for _, h := range hits {
		g, ok := groups[h.MeetingID]
		if !ok {
			g = &MeetingHits{
				MeetingID:    h.MeetingID,
				MeetingTitle: h.Title,
				CreatedAt:    h.CreatedAt,
				BestScore:    h.BM25Score,
			}
			groups[h.MeetingID] = g
			order = append(order, h.MeetingID)
		}
		if h.Title != "" && g.MeetingTitle == "" {
			g.MeetingTitle = h.Title
		}
		if !h.CreatedAt.IsZero() && g.CreatedAt.IsZero() {
			g.CreatedAt = h.CreatedAt
		}
		switch h.ResultType {
		case ResultTypeTitle:
			g.TitleMatch = true
		case ResultTypeTranscript:
			g.TranscriptCount++
		case ResultTypeDecision, ResultTypeAction, ResultTypeRisk:
			g.SummaryCount++
		case ResultTypeQuestion:
			g.SummaryCount++
			g.QuestionCount++
		case ResultTypeSummary:
			g.SummaryMatch = true
		}
		if h.BM25Score < g.BestScore {
			g.BestScore = h.BM25Score
		}
		if g.Snippet == "" && h.Snippet != "" {
			g.Snippet = h.Snippet
		}
		g.Hits = append(g.Hits, h)
	}

	out := make([]MeetingHits, 0, len(order))
	for _, id := range order {
		out = append(out, *groups[id])
	}
	sort.SliceStable(out, func(i, j int) bool {
		// Title match always beats no title match.
		if out[i].TitleMatch != out[j].TitleMatch {
			return out[i].TitleMatch
		}
		// Among non-title hits, a summary-body match beats a body-only
		// match — the user's words appearing in the summary signals
		// stronger topical relevance than a single transcript line.
		if out[i].SummaryMatch != out[j].SummaryMatch {
			return out[i].SummaryMatch
		}
		// Lower BM25 = more relevant.
		if out[i].BestScore != out[j].BestScore {
			return out[i].BestScore < out[j].BestScore
		}
		// Tiebreaker: most recent first.
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func (s *SearchIndex) DeleteFromIndex(meetingID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM meetings_fts WHERE meeting_id = ?`, meetingID); err != nil {
		return fmt.Errorf("delete from index: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM meetings_meta WHERE meeting_id = ?`, meetingID); err != nil {
		return fmt.Errorf("delete meta: %w", err)
	}
	return tx.Commit()
}

// NewSearchIndex opens (or creates) a standalone search index at path, owning
// the underlying connection. Use NewSearchIndexConn to build the index on a
// connection shared with other components (e.g. the one noto.sqlite handle the
// host also hands to the speaker store).
func NewSearchIndex(path string) (*SearchIndex, error) {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("create directory: %w", err)
		}
	}

	conn, err := db.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	if err := migrateSearchSchema(conn.DB); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return &SearchIndex{db: conn.DB, path: path, ownsConn: true}, nil
}

// NewSearchIndexConn builds a search index on an already-open connection. The
// caller retains ownership of the handle, so Close is a no-op — this lets the
// composition root share one noto.sqlite connection across the search index
// and the speaker store instead of opening the file twice.
func NewSearchIndexConn(conn *sql.DB) (*SearchIndex, error) {
	if err := migrateSearchSchema(conn); err != nil {
		return nil, err
	}
	return &SearchIndex{db: conn, ownsConn: false}, nil
}

// migrateSearchSchema creates/upgrades the FTS5 index and metadata tables on
// conn. It never closes conn — ownership stays with the caller.
func migrateSearchSchema(conn *sql.DB) error {
	if _, err := conn.Exec(`
		CREATE TABLE IF NOT EXISTS schema_kv(
			key   TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)
	`); err != nil {
		return fmt.Errorf("create schema_kv table: %w", err)
	}

	// FTS5 doesn't support ALTER ADD COLUMN. When the schema changes
	// we drop the index — the data is re-derivable by re-indexing
	// meetings (the service will reindex on next IndexMeeting calls,
	// or via the `reindex` job from config).
	//
	// We treat "no version row" as a stale schema too: pre-versioning
	// installs have a v1 meetings_fts table without the new columns,
	// and schema_kv was just created above so the row is missing on
	// that exact upgrade.
	var onDisk sql.NullString
	if err := conn.QueryRow(`SELECT value FROM schema_kv WHERE key='version'`).Scan(&onDisk); err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("read schema version: %w", err)
	}
	wantVersion := fmt.Sprintf("%d", schemaVersion)
	if !onDisk.Valid || onDisk.String != wantVersion {
		if _, err := conn.Exec(`DROP TABLE IF EXISTS meetings_fts`); err != nil {
			return fmt.Errorf("drop stale fts: %w", err)
		}
	}

	if _, err := conn.Exec(`
		CREATE VIRTUAL TABLE IF NOT EXISTS meetings_fts USING fts5(
			content,
			meeting_id,
			title,
			segment_text,
			speaker,
			decisions,
			actions,
			risks,
			questions,
			summary_body,
			segment_id,
			result_type,
			tokenize='unicode61'
		)
	`); err != nil {
		return fmt.Errorf("create FTS table: %w", err)
	}

	if _, err := conn.Exec(`
		INSERT INTO schema_kv(key, value) VALUES('version', ?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value
	`, wantVersion); err != nil {
		return fmt.Errorf("upsert schema version: %w", err)
	}

	if _, err := conn.Exec(`
		CREATE TABLE IF NOT EXISTS meetings_meta(
			meeting_id TEXT PRIMARY KEY,
			title      TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL DEFAULT 0
		)
	`); err != nil {
		return fmt.Errorf("create meta table: %w", err)
	}
	return nil
}

func (s *SearchIndex) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.ownsConn {
		return nil // borrowed connection; owner closes it
	}
	return s.db.Close()
}

type IndexMeetingInput struct {
	MeetingID          string
	Title              string
	ShortSummary       string
	CreatedAt          time.Time
	TranscriptSegments []TranscriptSegment
	Decisions          []SummaryItem
	ActionItems        []ActionItem
	Risks              []SummaryItem
	OpenQuestions      []SummaryItem
}

type TranscriptSegment struct {
	SegmentID string
	Speaker   string
	Text      string
	Timestamp float64
}

type SummaryItem struct {
	Text       string
	SpeakerIDs []string
}

type ActionItem struct {
	Text  string
	Owner string
}

type Meeting struct {
	MeetingID          string
	Title              string
	ShortSummary       string
	CreatedAt          time.Time
	TranscriptSegments []TranscriptSegment
	Decisions          []SummaryItem
	ActionItems        []ActionItem
	Risks              []SummaryItem
	OpenQuestions      []SummaryItem
}

func (s *SearchIndex) IndexMeetingFromInput(input *IndexMeetingInput) error {
	meeting := &Meeting{
		MeetingID:          input.MeetingID,
		Title:              input.Title,
		ShortSummary:       input.ShortSummary,
		CreatedAt:          input.CreatedAt,
		TranscriptSegments: input.TranscriptSegments,
		Decisions:          input.Decisions,
		ActionItems:        input.ActionItems,
		Risks:              input.Risks,
		OpenQuestions:      input.OpenQuestions,
	}
	return s.IndexMeeting(meeting)
}
