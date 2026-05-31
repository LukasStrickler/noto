package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/lukasstrickler/noto/internal/testutil"
	"github.com/lukasstrickler/noto/internal/transport/notoapi"
	"github.com/lukasstrickler/noto/internal/ui/tui/keys"
	"github.com/lukasstrickler/noto/internal/ui/tui/theme"
)

// This is the flow that was impossible before testutil.FakeClient: drive a
// screen through the real load path (fetch cmd -> message -> update ->
// view) against a programmable client, instead of poking struct fields.
func TestDashboardLoadsMeetingsThroughClient(t *testing.T) {
	fake := testutil.NewFakeClient()
	fake.Meetings = []notoapi.Meeting{
		{ID: "m1", Title: "Quarterly planning", Status: notoapi.StatusSummarized, CreatedAt: time.Date(2026, 5, 20, 9, 0, 0, 0, time.UTC)},
		{ID: "m2", Title: "Vendor sync", Status: notoapi.StatusRecorded, CreatedAt: time.Date(2026, 5, 19, 9, 0, 0, 0, time.UTC)},
	}

	ctx := screenCtx{
		ctx:    context.Background(),
		client: fake,
		keys:   keys.New(),
		styles: theme.NewStyles(),
		width:  200, // wide enough that the 1/3 list column doesn't truncate titles
		height: 30,
	}

	m := newDashboardScreen().(*dashboardScreen)

	// Run the fetch command the screen would issue on enter(), then feed
	// the resulting message back through update — exactly the runtime loop.
	msg := fetchMeetings(ctx)()
	updated, _ := m.update(ctx, msg)
	m = updated.(*dashboardScreen)

	if m.loading {
		t.Fatal("dashboard still loading after meetings message")
	}
	if len(m.all) != 2 {
		t.Fatalf("loaded %d meetings; want 2", len(m.all))
	}

	out := ansi.Strip(m.view(ctx))
	for _, want := range []string{"Quarterly planning", "Vendor sync"} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered dashboard missing %q", want)
		}
	}
}
