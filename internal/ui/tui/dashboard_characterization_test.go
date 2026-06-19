package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/lukasstrickler/noto/internal/transport/notoapi"
	"github.com/lukasstrickler/noto/internal/ui/tui/theme"
)

// Meetings that still have speakers to identify get an amber left bar down the
// whole row so the eye is pulled to them; resolved meetings stay quiet. The bar
// must PERSIST when the flagged row is selected — it sits beside the cursor (▍▸)
// instead of vanishing under the highlight, which was the original bug.
func TestDashboardAttentionBarFlagsUnresolvedRows(t *testing.T) {
	m := newDashboardScreen().(*dashboardScreen)
	m.loading = false
	base := time.Date(2026, 5, 20, 9, 0, 0, 0, time.UTC)
	// Distinct short prefixes so the titles survive list clipping.
	m.all = []notoapi.Meeting{
		{ID: "m1", Title: "Resolved", Status: notoapi.StatusSummarized, CreatedAt: base,
			Identity: &notoapi.SpeakerIdentitySummary{Resolved: 3}},
		{ID: "m2", Title: "Flagged", Status: notoapi.StatusTranscribed, CreatedAt: base,
			Identity: &notoapi.SpeakerIdentitySummary{Resolved: 1, Unset: 2}},
	}

	// Cursor parked ON the flagged row: the bar and the cursor must coexist.
	m.cursor = 1
	out := ansi.Strip(m.view(testScreenCtx()))
	var flaggedLine string
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "Flagged") {
			flaggedLine = line
		}
		if strings.Contains(line, "Resolved") && strings.Contains(line, "▍") {
			t.Errorf("resolved meeting row must not carry an attention bar: %q", line)
		}
	}
	if !strings.Contains(flaggedLine, "▍") || !strings.Contains(flaggedLine, "▸") {
		t.Errorf("selected flagged row must show the attention bar (▍) next to the cursor (▸); got %q", flaggedLine)
	}
}

// The structural test above proves the ▍/▸ glyphs survive; this one proves they
// survive for the right REASON — the selected flagged row is painted with the
// Primary fill (RowSelected) AND still carries the amber Warning bar on the SAME
// raw line. Without protect=1 in fillRow the amber SGR would be flattened away by
// ansi.Strip and only the fill would remain — the exact bug the user reported.
func TestDashboardSelectedFlaggedRowFillKeepsAmber(t *testing.T) {
	const (
		primaryBG = "48;2;96;165;250" // RowSelected fill (theme Primary)
		warningFG = "38;2;251;191;36" // the amber ▍ bar (theme Warning)
	)
	m := newDashboardScreen().(*dashboardScreen)
	m.loading = false
	base := time.Date(2026, 5, 20, 9, 0, 0, 0, time.UTC)
	m.all = []notoapi.Meeting{
		{ID: "m1", Title: "Flagged", Status: notoapi.StatusTranscribed, CreatedAt: base,
			Identity: &notoapi.SpeakerIdentitySummary{Resolved: 1, Unset: 2}},
	}
	m.cursor = 0 // selected, and flagged → ▍▸ + fill must coexist

	raw := rawLineWith(t, m.view(testScreenCtx()), "Flagged")
	if !strings.Contains(raw, primaryBG) {
		t.Errorf("selected row missing the Primary fill (%s); line=%q", primaryBG, raw)
	}
	if !strings.Contains(raw, warningFG) {
		t.Errorf("selected flagged row dropped the amber bar (%s) under the fill; line=%q", warningFG, raw)
	}
}

func TestDashboardRender_DoesNotPanic(t *testing.T) {
	m := newDashboardScreen().(*dashboardScreen)
	m.loading = false
	m.all = []notoapi.Meeting{
		{
			ID:        "m1",
			Title:     "Test Meeting",
			Status:    notoapi.StatusSummarized,
			CreatedAt: time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC),
		},
	}
	m.cursor = 0

	ctx := testScreenCtx()

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("dashboard view panicked: %v", r)
		}
	}()

	_ = m.view(ctx)
}

func TestDashboardRender_EmptyStateDoesNotPanic(t *testing.T) {
	m := newDashboardScreen().(*dashboardScreen)
	m.loading = false
	m.all = nil

	ctx := testScreenCtx()

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("dashboard view panicked on empty state: %v", r)
		}
	}()

	_ = m.view(ctx)
}

func TestDashboardRender_LoadingStateDoesNotPanic(t *testing.T) {
	m := newDashboardScreen().(*dashboardScreen)
	m.loading = true

	ctx := testScreenCtx()

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("dashboard view panicked on loading state: %v", r)
		}
	}()

	_ = m.view(ctx)
}

func TestDashboardRender_ErrorStateDoesNotPanic(t *testing.T) {
	m := newDashboardScreen().(*dashboardScreen)
	m.loading = false
	m.err = &testError{msg: "something went wrong"}

	ctx := testScreenCtx()

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("dashboard view panicked on error state: %v", r)
		}
	}()

	_ = m.view(ctx)
}

func TestDashboardRender_SearchResultsDoNotPanic(t *testing.T) {
	m := newDashboardScreen().(*dashboardScreen)
	m.loading = false
	m.query = "test"
	m.matched = []notoapi.MeetingHits{
		{
			MeetingID:    "m1",
			MeetingTitle: "Test Meeting",
			TopHits:      []notoapi.SearchHit{},
		},
	}
	m.cursor = 0

	ctx := testScreenCtx()

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("dashboard view panicked on search results: %v", r)
		}
	}()

	_ = m.view(ctx)
}

func TestDashboardRender_WideLayout(t *testing.T) {
	m := newDashboardScreen().(*dashboardScreen)
	m.loading = false
	m.all = []notoapi.Meeting{
		{
			ID:        "m1",
			Title:     "Planning",
			Status:    notoapi.StatusSummarized,
			CreatedAt: time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC),
		},
		{
			ID:        "m2",
			Title:     "Retro",
			Status:    notoapi.StatusRecorded,
			CreatedAt: time.Date(2026, 5, 19, 14, 0, 0, 0, time.UTC),
		},
	}

	ctx := screenCtx{
		ctx:    testScreenCtx().ctx,
		keys:   testScreenCtx().keys,
		styles: theme.NewStyles(),
		width:  120,
		height: 30,
	}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("dashboard wide layout panicked: %v", r)
		}
	}()

	output := m.view(ctx)

	stripped := ansi.Strip(output)
	if !strings.Contains(stripped, "Planning") {
		t.Error("wide layout output missing meeting title")
	}
	if !strings.Contains(stripped, "Retro") {
		t.Error("wide layout output missing second meeting title")
	}
}

func TestDashboardRender_NarrowLayout(t *testing.T) {
	m := newDashboardScreen().(*dashboardScreen)
	m.loading = false
	m.all = []notoapi.Meeting{
		{
			ID:        "m1",
			Title:     "Narrow Test",
			Status:    notoapi.StatusSummarized,
			CreatedAt: time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC),
		},
	}

	ctx := screenCtx{
		ctx:    testScreenCtx().ctx,
		keys:   testScreenCtx().keys,
		styles: theme.NewStyles(),
		width:  40,
		height: 20,
	}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("dashboard narrow layout panicked: %v", r)
		}
	}()

	_ = m.view(ctx)
}

func TestDashboardRender_WithJobsAndRecording(t *testing.T) {
	m := newDashboardScreen().(*dashboardScreen)
	m.loading = false
	m.all = []notoapi.Meeting{
		{
			ID:        "m1",
			Title:     "With Activity",
			Status:    notoapi.StatusRecording,
			CreatedAt: time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC),
		},
	}
	m.jobs = []notoapi.Job{
		{ID: "j1", Kind: notoapi.JobTranscribe, Status: notoapi.JobRunning, Progress: 0.5},
		{ID: "j2", Kind: notoapi.JobSummarize, Status: notoapi.JobSucceeded, Progress: 1.0},
	}
	m.rec = notoapi.RecordingState{Active: true, ElapsedSec: 120, Title: "Live Meeting"}

	ctx := testScreenCtx()

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("dashboard with jobs/recording panicked: %v", r)
		}
	}()

	_ = m.view(ctx)
}

func TestDashboardBottomStripHeight_AllIdle(t *testing.T) {
	m := &dashboardScreen{rec: notoapi.RecordingState{Active: false}, jobs: nil}
	if h := m.bottomStripHeight(); h != 0 {
		t.Errorf("bottomStripHeight with no activity = %d; want 0", h)
	}
}

func TestDashboardBottomStripHeight_WithRecording(t *testing.T) {
	m := &dashboardScreen{rec: notoapi.RecordingState{Active: true}, jobs: nil}
	if h := m.bottomStripHeight(); h != 6 {
		t.Errorf("bottomStripHeight with recording = %d; want 6", h)
	}
}

func TestDashboardBottomStripHeight_WithManyJobs(t *testing.T) {
	m := &dashboardScreen{
		rec:  notoapi.RecordingState{Active: false},
		jobs: []notoapi.Job{{}, {}, {}, {}},
	}
	if h := m.bottomStripHeight(); h != 8 {
		t.Errorf("bottomStripHeight with 4 jobs = %d; want 8", h)
	}
}

type testError struct {
	msg string
}

func (e *testError) Error() string { return e.msg }
