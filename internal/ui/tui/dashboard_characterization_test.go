package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/lukasstrickler/noto/internal/transport/notoapi"
	"github.com/lukasstrickler/noto/internal/ui/tui/theme"
)

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
