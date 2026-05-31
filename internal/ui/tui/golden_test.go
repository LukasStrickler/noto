package tui

import (
	"context"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/lukasstrickler/noto/internal/transport/notoapi"
	"github.com/lukasstrickler/noto/internal/ui/tui/keys"
	"github.com/lukasstrickler/noto/internal/ui/tui/theme"
)

// updateGolden regenerates the TUI frame fixtures:
//
//	go test ./internal/tui/ -run Golden -update
var updateGolden = flag.Bool("update", false, "update TUI golden frames")

func goldenMeetings() []notoapi.Meeting {
	return []notoapi.Meeting{
		{ID: "m1", Title: "Quarterly planning", Status: notoapi.StatusSummarized, CreatedAt: time.Date(2026, 5, 20, 9, 0, 0, 0, time.UTC), DecisionCount: 2, ActionCount: 3},
		{ID: "m2", Title: "Vendor benchmark", Status: notoapi.StatusRecorded, CreatedAt: time.Date(2026, 5, 19, 14, 0, 0, 0, time.UTC)},
		{ID: "m3", Title: "Hiring loop debrief", Status: notoapi.StatusTranscribed, CreatedAt: time.Date(2026, 5, 18, 11, 0, 0, 0, time.UTC)},
	}
}

// TestDashboardGoldenFrame snapshots the rendered (ANSI-stripped) layout at
// fixed sizes. Stripping color keeps the golden about *structure* — the
// thing the layout solver controls — so a column-width or wrapping
// regression shows up as a clean diff instead of a "doesn't panic" pass.
func TestDashboardGoldenFrame(t *testing.T) {
	frames := []struct {
		name string
		w, h int
	}{
		{"dashboard_wide", 120, 30},
		{"dashboard_narrow", 70, 24},
	}
	for _, f := range frames {
		t.Run(f.name, func(t *testing.T) {
			m := newDashboardScreen().(*dashboardScreen)
			m.loading = false
			m.all = goldenMeetings()
			ctx := screenCtx{
				ctx:    context.Background(),
				keys:   keys.New(),
				styles: theme.NewStyles(),
				width:  f.w,
				height: f.h,
			}
			assertGoldenFrame(t, f.name, ansi.Strip(m.view(ctx)))
		})
	}
}

func assertGoldenFrame(t *testing.T, name, got string) {
	t.Helper()
	golden := filepath.Join("testdata", "frames", name+".golden")
	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(golden), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden (run with -update to create): %v", err)
	}
	if got != string(want) {
		t.Errorf("%s frame drifted from golden.\n--- got ---\n%s\n--- want ---\n%s", name, got, string(want))
	}
}
