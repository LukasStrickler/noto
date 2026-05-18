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

	_ "modernc.org/sqlite"
)

type SearchIndex struct {
	db   *sql.DB
	mu   sync.RWMutex
	path string
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
	TranscriptCount int
	SummaryCount    int
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
)

// FTS5 column order for bm25() weights. KEEP IN SYNC with the
// CREATE VIRTUAL TABLE statement in NewSearchIndex.
const (
	colContent     = 0
	colMeetingID   = 1
	colTitle       = 2
	colSegmentText = 3
	colSpeaker     = 4
	colDecisions   = 5
	colActions     = 6
	colRisks       = 7
	colSegmentID   = 8
	colResultType  = 9
	colCount       = 10
)

// Per-column BM25 weights. Title is heavily boosted so a title-match
// always outranks a body-only match. ID/segment-id/result-type get
// weight 0 — they're stored for filtering, not relevance.
var columnWeights = [colCount]float64{
	colContent:     1.0,
	colMeetingID:   0.0,
	colTitle:       5.0,
	colSegmentText: 1.0,
	colSpeaker:     0.5,
	colDecisions:   2.0,
	colActions:     2.0,
	colRisks:       2.0,
	colSegmentID:   0.0,
	colResultType:  0.0,
}

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

	if meeting.Title != "" {
		if err := s.insertEntry(tx, meeting.MeetingID, meeting.Title, "", "", meeting.Title, "", "", "", "title"); err != nil {
			return err
		}
	}

	for _, seg := range meeting.TranscriptSegments {
		if seg.Text != "" {
			if err := s.insertEntry(tx, meeting.MeetingID, meeting.Title, seg.Text, seg.Speaker, "", "", "", seg.SegmentID, "transcript"); err != nil {
				return err
			}
		}
	}

	for _, dec := range meeting.Decisions {
		if dec.Text != "" {
			if err := s.insertEntry(tx, meeting.MeetingID, meeting.Title, "", "", "", "", dec.Text, "", "decision"); err != nil {
				return err
			}
		}
	}

	for _, act := range meeting.ActionItems {
		if act.Text != "" {
			text := act.Text + " " + act.Owner
			if err := s.insertEntry(tx, meeting.MeetingID, meeting.Title, "", "", "", text, "", "", "action"); err != nil {
				return err
			}
		}
	}

	for _, risk := range meeting.Risks {
		if risk.Text != "" {
			if err := s.insertEntry(tx, meeting.MeetingID, meeting.Title, "", "", "", "", risk.Text, "", "risk"); err != nil {
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

	if _, err := s.db.Exec(`PRAGMA optimize`); err != nil {
		return fmt.Errorf("optimize index: %w", err)
	}

	return nil
}

func (s *SearchIndex) insertEntry(tx *sql.Tx, meetingID, title, segmentText, speaker, decisions, actions, risks, segmentID, resultType string) error {
	content := buildContent(title, segmentText, speaker, decisions, actions, risks)
	_, err := tx.Exec(
		`INSERT INTO meetings_fts(content, meeting_id, title, segment_text, speaker, decisions, actions, risks, segment_id, result_type) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		content, meetingID, title, segmentText, speaker, decisions, actions, risks, segmentID, resultType,
	)
	if err != nil {
		return fmt.Errorf("insert entry: %w", err)
	}
	return nil
}

func buildContent(title, segmentText, speaker, decisions, actions, risks string) string {
	var parts []string
	if title != "" {
		parts = append(parts, title)
	}
	if segmentText != "" {
		parts = append(parts, segmentText)
	}
	if speaker != "" {
		parts = append(parts, speaker)
	}
	if decisions != "" {
		parts = append(parts, decisions)
	}
	if actions != "" {
		parts = append(parts, actions)
	}
	if risks != "" {
		parts = append(parts, risks)
	}
	return strings.Join(parts, " ")
}

// sanitizeFTS5Query strips characters FTS5 would interpret as operators
// (`*`, `:`, `(`, `)`, `"`, `-`, `+`, `^`, `.`, etc.) and re-emits the
// remaining words as quoted phrases. An empty result means the query
// had no usable tokens and the caller should return zero results
// instead of erroring.
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
	quoted := make([]string, len(tokens))
	for i, t := range tokens {
		quoted[i] = `"` + t + `"`
	}
	return strings.Join(quoted, " ")
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

	weightArgs := make([]string, 0, colCount)
	for _, w := range columnWeights {
		weightArgs = append(weightArgs, fmt.Sprintf("%.2f", w))
	}

	sqlText := `
		SELECT
			f.meeting_id,
			f.title,
			f.segment_text,
			f.speaker,
			f.decisions,
			f.actions,
			f.risks,
			f.segment_id,
			f.result_type,
			bm25(meetings_fts, ` + strings.Join(weightArgs, ", ") + `) AS rank,
			snippet(meetings_fts, -1, '**', '**', '...', 16) AS snippet,
			COALESCE(m.created_at, 0) AS created_ns
		FROM meetings_fts f
		LEFT JOIN meetings_meta m ON m.meeting_id = f.meeting_id
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
		var title, segmentText, speaker, decisions, actions, risks, segmentID, resultType, snippet sql.NullString
		var rank sql.NullFloat64
		var createdNS sql.NullInt64

		if err := rows.Scan(&r.MeetingID, &title, &segmentText, &speaker, &decisions, &actions, &risks, &segmentID, &resultType, &rank, &snippet, &createdNS); err != nil {
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
			if title.String != "" {
				r.Snippet = title.String
			} else if segmentText.String != "" {
				r.Snippet = segmentText.String
			} else if decisions.String != "" {
				r.Snippet = decisions.String
			} else if actions.String != "" {
				r.Snippet = actions.String
			} else if risks.String != "" {
				r.Snippet = risks.String
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
	if len(hits) == 0 {
		return nil, nil
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
	return out, nil
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

func NewSearchIndex(path string) (*SearchIndex, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create directory: %w", err)
	}

	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	_, err = db.Exec(`
		CREATE VIRTUAL TABLE IF NOT EXISTS meetings_fts USING fts5(
			content,
			meeting_id,
			title,
			segment_text,
			speaker,
			decisions,
			actions,
			risks,
			segment_id,
			result_type,
			tokenize='unicode61'
		)
	`)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("create FTS table: %w", err)
	}

	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS meetings_meta(
			meeting_id TEXT PRIMARY KEY,
			title      TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL DEFAULT 0
		)
	`)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("create meta table: %w", err)
	}

	return &SearchIndex{
		db:   db,
		path: path,
	}, nil
}

func (s *SearchIndex) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.db.Close()
}

type IndexMeetingInput struct {
	MeetingID          string
	Title              string
	CreatedAt          time.Time
	TranscriptSegments []TranscriptSegment
	Decisions          []SummaryItem
	ActionItems        []ActionItem
	Risks              []SummaryItem
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
	CreatedAt          time.Time
	TranscriptSegments []TranscriptSegment
	Decisions          []SummaryItem
	ActionItems        []ActionItem
	Risks              []SummaryItem
}

func (s *SearchIndex) IndexMeetingFromInput(input *IndexMeetingInput) error {
	meeting := &Meeting{
		MeetingID:          input.MeetingID,
		Title:              input.Title,
		CreatedAt:          input.CreatedAt,
		TranscriptSegments: input.TranscriptSegments,
		Decisions:          input.Decisions,
		ActionItems:        input.ActionItems,
		Risks:              input.Risks,
	}
	return s.IndexMeeting(meeting)
}
