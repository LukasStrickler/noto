package search

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

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
}

type ResultType string

const (
	ResultTypeTranscript ResultType = "transcript"
	ResultTypeDecision  ResultType = "decision"
	ResultTypeAction    ResultType = "action"
	ResultTypeRisk      ResultType = "risk"
)

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
		if err := s.insertEntry(tx, meeting.MeetingID, meeting.Title, "", "", "", "", "", "title"); err != nil {
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
			if err := s.insertEntry(tx, meeting.MeetingID, meeting.Title, "", "", dec.Text, "", "", "", "decision"); err != nil {
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

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}

	return nil
}

func (s *SearchIndex) insertEntry(tx *sql.Tx, meetingID, title, segmentText, speaker, decisions, actions, risks, segmentID, resultType string) error {
	content := buildContent(title, segmentText, speaker, decisions, actions, risks)
	_, err := tx.Exec(
		`INSERT INTO meetings_fts(content, meeting_id, segment_text, speaker, decisions, actions, risks, segment_id, result_type) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		content, meetingID, segmentText, speaker, decisions, actions, risks, segmentID, resultType,
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

func (s *SearchIndex) Search(query string) ([]SearchResult, error) {
	if query == "" {
		return nil, fmt.Errorf("query required")
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(`
		SELECT 
			meeting_id,
			segment_text,
			speaker,
			decisions,
			actions,
			risks,
			segment_id,
			result_type,
			bm25(meetings_fts) as rank,
			snippet(meetings_fts, 0, '**', '**', '...', 32) as snippet
		FROM meetings_fts
		WHERE meetings_fts MATCH ?
		ORDER BY rank
		LIMIT 100
	`, query)
	if err != nil {
		return nil, fmt.Errorf("search query: %w", err)
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var r SearchResult
		var segmentText, speaker, decisions, actions, risks, segmentID, resultType, snippet sql.NullString
		var rank sql.NullFloat64

		if err := rows.Scan(&r.MeetingID, &segmentText, &speaker, &decisions, &actions, &risks, &segmentID, &resultType, &rank, &snippet); err != nil {
			return nil, fmt.Errorf("scan row: %w", err)
		}

		r.SegmentText = segmentText.String
		r.Speaker = speaker.String
		r.Snippet = snippet.String
		r.SegmentID = segmentID.String
		r.ResultType = ResultType(resultType.String)
		if rank.Valid {
			r.BM25Score = rank.Float64
		}

		if r.Snippet == "" {
			if decisions.String != "" {
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

func (s *SearchIndex) DeleteFromIndex(meetingID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(`DELETE FROM meetings_fts WHERE meeting_id = ?`, meetingID)
	if err != nil {
		return fmt.Errorf("delete from index: %w", err)
	}

	return nil
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

	_, err = db.Exec(`PRAGMA journal_mode=WAL`)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("enable WAL: %w", err)
	}

	_, err = db.Exec(`
		CREATE VIRTUAL TABLE IF NOT EXISTS meetings_fts USING fts5(
			content,
			meeting_id,
			segment_text,
			speaker,
			decisions,
			actions,
			risks,
			segment_id,
			result_type,
			tokenize='unicode61',
			content='',
			contentless_delete=1
		)
	`)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("create FTS table: %w", err)
	}

	_, err = db.Exec(`CREATE INDEX IF NOT EXISTS idx_meeting_id ON meetings_fts(meeting_id)`)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("create index: %w", err)
	}

	return &SearchIndex{
		db:  db,
		path: path,
	}, nil
}

func (s *SearchIndex) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.db.Close()
}

type IndexMeetingInput struct {
	MeetingID        string
	Title           string
	TranscriptSegments []TranscriptSegment
	Decisions       []SummaryItem
	ActionItems     []ActionItem
	Risks           []SummaryItem
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
	MeetingID         string
	Title            string
	TranscriptSegments []TranscriptSegment
	Decisions        []SummaryItem
	ActionItems      []ActionItem
	Risks            []SummaryItem
}

func (s *SearchIndex) IndexMeetingFromInput(input *IndexMeetingInput) error {
	meeting := &Meeting{
		MeetingID:         input.MeetingID,
		Title:            input.Title,
		TranscriptSegments: input.TranscriptSegments,
		Decisions:        input.Decisions,
		ActionItems:      input.ActionItems,
		Risks:            input.Risks,
	}
	return s.IndexMeeting(meeting)
}
