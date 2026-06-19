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
		{"dashboard_wide", 140, 30},
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

func goldenProfiles() []notoapi.SpeakerProfile {
	seen := time.Date(2026, 5, 20, 9, 0, 0, 0, time.UTC)
	return []notoapi.SpeakerProfile{
		{ID: "p1", DisplayName: "Alice Nguyen", Pronouns: "she/her", MeetingCount: 2, LastSeenAt: &seen,
			Notes:        "Eng lead. Owns roadmap + the search v2 ship.",
			Affiliations: []notoapi.Affiliation{{Context: "Work", Organization: "noto", Email: "alice@noto.app"}},
			Meetings: []notoapi.ProfileMeetingRef{
				{MeetingID: "m1", Title: "Q2 Roadmap Sync", CreatedAt: seen, MatchStatus: "auto"},
				{MeetingID: "m8", Title: "Design review: Search v2", CreatedAt: seen, MatchStatus: "auto"},
			}},
		{ID: "p2", DisplayName: "Bob Martins", Pronouns: "he/him", MeetingCount: 2, LastSeenAt: &seen},
		{ID: "p3", DisplayName: "Carol Reyes", Pronouns: "she/her", MeetingCount: 1, LastSeenAt: &seen},
		{ID: "p4", DisplayName: "Dana Okafor", Pronouns: "she/her", MeetingCount: 1},
	}
}

// TestPeopleGoldenFrame snapshots the People screen layout — directory column
// alignment + detail pane — so a column or wrapping regression is a clean diff.
func TestPeopleGoldenFrame(t *testing.T) {
	frames := []struct {
		name string
		w, h int
	}{
		{"people_wide", 140, 30},
		{"people_narrow", 70, 24},
	}
	for _, f := range frames {
		t.Run(f.name, func(t *testing.T) {
			p := newPeopleScreen().(*peopleScreen)
			p.loading = false
			p.profiles = goldenProfiles()
			p.detail = p.profiles[0]
			p.detailID = "p1"
			ctx := screenCtx{
				ctx:    context.Background(),
				keys:   keys.New(),
				styles: theme.NewStyles(),
				width:  f.w,
				height: f.h,
			}
			assertGoldenFrame(t, f.name, ansi.Strip(p.view(ctx)))
		})
	}
}

// TestPeopleStateGoldenFrames snapshots the two states this change adds — the
// meetings list focused (Tab) and the merge confirm overlay — so the marker
// highlight, focus hints, and the centered confirm box stay put.
func TestPeopleStateGoldenFrames(t *testing.T) {
	base := func() *peopleScreen {
		p := newPeopleScreen().(*peopleScreen)
		p.loading = false
		p.profiles = goldenProfiles()
		p.detail = p.profiles[0]
		p.detailID = "p1"
		return p
	}
	ctx := screenCtx{
		ctx:    context.Background(),
		keys:   keys.New(),
		styles: theme.NewStyles(),
		width:  140,
		height: 30,
	}

	t.Run("people_meetings_focus", func(t *testing.T) {
		p := base()
		p.focusMeetings = true
		p.meetingCur = 1
		assertGoldenFrame(t, "people_meetings_focus", ansi.Strip(p.view(ctx)))
	})
	t.Run("people_merge_confirm", func(t *testing.T) {
		p := base()
		p.merging = true
		p.cursor = 0   // fold Alice
		p.mergeIdx = 1 // into Bob
		p.confirm = confirmMerge
		assertGoldenFrame(t, "people_merge_confirm", ansi.Strip(p.view(ctx)))
	})
}

// TestDashboardIdentityGoldenFrame locks the per-meeting identity cluster on the
// browse rows: a single amber ⚑N counting speakers still to identify (likely +
// new + not-set), and NOTHING when fully resolved (the list flags only
// outstanding work, no "done" badge).
func TestDashboardIdentityGoldenFrame(t *testing.T) {
	m := newDashboardScreen().(*dashboardScreen)
	m.loading = false
	base := time.Date(2026, 5, 20, 9, 0, 0, 0, time.UTC)
	m.all = []notoapi.Meeting{
		{ID: "m1", Title: "Design review: Search v2", Status: notoapi.StatusSummarized, CreatedAt: base, DecisionCount: 2,
			Identity: &notoapi.SpeakerIdentitySummary{Resolved: 3, Likely: 1, New: 1}},
		{ID: "m2", Title: "Security incident retro", Status: notoapi.StatusTranscribed, CreatedAt: base,
			Identity: &notoapi.SpeakerIdentitySummary{Resolved: 1, Unset: 2}},
		{ID: "m3", Title: "Quarterly planning", Status: notoapi.StatusSummarized, CreatedAt: base, ActionCount: 3,
			Identity: &notoapi.SpeakerIdentitySummary{Resolved: 4}},
	}
	ctx := screenCtx{
		ctx: context.Background(), keys: keys.New(), styles: theme.NewStyles(),
		width: 140, height: 30,
	}
	assertGoldenFrame(t, "dashboard_identity", ansi.Strip(m.view(ctx)))
}

// TestSpeakersTabGoldenFrame locks the Speakers tab: a ● swatch per row, names
// at their confidence tier (bold confirmed person, ~guess? for a likely match,
// plain token for unknown), and — crucially — the aligned amber ⚑ attention flag
// with NO "✓ set" success badge, so talk-bars and stats line up across rows.
func TestSpeakersTabGoldenFrame(t *testing.T) {
	d := newDetailPane()
	d.id_ = "m1"
	d.tab = tabSpeakers
	d.meeting = notoapi.Meeting{ID: "m1", Title: "Design review: Search v2", Status: notoapi.StatusSummarized, CreatedAt: time.Date(2026, 5, 20, 9, 0, 0, 0, time.UTC)}
	d.transcript = notoapi.Transcript{
		MeetingID: "m1",
		Speakers: []notoapi.Speaker{
			{ID: "A", Label: "Speaker A"}, {ID: "B", Label: "Speaker B"}, {ID: "C", Label: "Speaker C"},
		},
		Segments: []notoapi.TranscriptSegment{
			{ID: "s1", SpeakerID: "A", Speaker: "Speaker A", StartSec: 0, EndSec: 30, Text: "lock the ranking model"},
			{ID: "s2", SpeakerID: "B", Speaker: "Speaker B", StartSec: 30, EndSec: 50, Text: "owns rollout and eval"},
			{ID: "s3", SpeakerID: "C", Speaker: "Speaker C", StartSec: 50, EndSec: 60, Text: "mobile latency risk"},
		},
	}
	d.speakers = computeSpeakerStats(d.transcript)
	alice, carol := "p1", "p3"
	d.mappings = map[string]notoapi.MeetingSpeakerMapping{
		"A": {MeetingSpeakerID: "A", MatchStatus: "manual", ProfileID: &alice, ProfileName: "Alice Nguyen"},
		"B": {MeetingSpeakerID: "B", MatchStatus: "pending", ProfileID: &carol, ProfileName: "Carol Reyes"},
		"C": {MeetingSpeakerID: "C", MatchStatus: "unmatched"},
	}
	ctx := screenCtx{ctx: context.Background(), keys: keys.New(), styles: theme.NewStyles(), width: 96, height: 26}
	assertGoldenFrame(t, "speakers_tab", ansi.Strip(d.view(ctx, 88, 24, 0, 0, nil)))
}

// TestAssignDialogGoldenFrame locks the "Identify speaker" overlay: the
// currently-linked person, ranked candidates (score/reason), the rest of the
// directory, and the create-new action.
func TestAssignDialogGoldenFrame(t *testing.T) {
	d := newDetailPane()
	d.id_ = "m1"
	d.tab = tabSpeakers
	d.speakers = []speakerStat{{ID: "A", Name: "Speaker A"}}
	bob := "p2"
	d.mappings = map[string]notoapi.MeetingSpeakerMapping{
		"A": {MeetingSpeakerID: "A", MatchStatus: "manual", ProfileID: &bob, ProfileName: "Bob Martins",
			Candidates: []notoapi.SpeakerCandidate{
				{ProfileID: "p3", DisplayName: "Carol Reyes", Score: 0.63, Reason: "frequent co-attendee", Rank: 1},
				{ProfileID: "p5", DisplayName: "Riya Shah", Score: 0.58, Rank: 2},
			}},
	}
	d.directory = goldenProfiles()
	d.assignOpen = true
	ctx := screenCtx{
		ctx: context.Background(), keys: keys.New(), styles: theme.NewStyles(),
		width: 90, height: 30,
	}
	assertGoldenFrame(t, "assign_dialog", ansi.Strip(d.assignDialogView(ctx)))
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
