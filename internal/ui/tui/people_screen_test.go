package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/lukasstrickler/noto/internal/testutil"
	"github.com/lukasstrickler/noto/internal/transport/notoapi"
	"github.com/lukasstrickler/noto/internal/ui/tui/keys"
	"github.com/lukasstrickler/noto/internal/ui/tui/theme"
)

func peopleCtx(fake *testutil.FakeClient) screenCtx {
	return screenCtx{
		ctx:    context.Background(),
		client: fake,
		keys:   keys.New(),
		styles: theme.NewStyles(),
		width:  200,
		height: 30,
	}
}

// The People screen loads profiles through the client and renders each
// person's name + meeting count, exercising the fetch→msg→update→view loop.
func TestPeopleScreenLoadsAndRenders(t *testing.T) {
	seen := time.Date(2026, 5, 20, 9, 0, 0, 0, time.UTC)
	fake := testutil.NewFakeClient()
	fake.Profiles = []notoapi.SpeakerProfile{
		{ID: "p1", DisplayName: "Alice", MeetingCount: 2, LastSeenAt: &seen},
		{ID: "p2", DisplayName: "Bob", MeetingCount: 1},
	}
	ctx := peopleCtx(fake)

	p := newPeopleScreen().(*peopleScreen)
	msg := p.enter(ctx, "")()
	updated, _ := p.update(ctx, msg)
	p = updated.(*peopleScreen)

	if p.loading {
		t.Fatal("people screen still loading after profiles message")
	}
	out := ansi.Strip(p.view(ctx))
	for _, want := range []string{"Alice", "Bob", "2 mtg"} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered people screen missing %q\n%s", want, out)
		}
	}
}

// People with unconfirmed (auto-created) mappings surface a "⚑ N to review"
// notification in the directory header and an amber bar on their row, so the
// page itself shows there's something to act on.
func TestPeopleScreenReviewNotification(t *testing.T) {
	fake := testutil.NewFakeClient()
	fake.Profiles = []notoapi.SpeakerProfile{
		{ID: "p1", DisplayName: "Alice Nguyen", MeetingCount: 2},
		{ID: "p2", DisplayName: "Speaker 3", MeetingCount: 1, UnconfirmedMeetings: 1},
	}
	ctx := peopleCtx(fake)

	p := newPeopleScreen().(*peopleScreen)
	p = mustUpdate(p.update(ctx, p.enter(ctx, "")()))
	p.cursor = 0 // keep the cursor off the review row so its bar shows

	out := ansi.Strip(p.view(ctx))
	if !strings.Contains(out, "to review") {
		t.Errorf("expected a 'to review' notification in the directory header:\n%s", out)
	}
	if !strings.Contains(out, "▍") {
		t.Errorf("expected an amber attention bar on the unconfirmed person row:\n%s", out)
	}

	// With every person confirmed the notification disappears.
	fake.Profiles[1].UnconfirmedMeetings = 0
	p2 := newPeopleScreen().(*peopleScreen)
	p2 = mustUpdate(p2.update(ctx, p2.enter(ctx, "")()))
	if strings.Contains(ansi.Strip(p2.view(ctx)), "to review") {
		t.Error("review notification should be hidden when nothing is unconfirmed")
	}
}

func mustUpdate(updated screen, _ tea.Cmd) *peopleScreen {
	return updated.(*peopleScreen)
}

// Editing a person and pressing Enter issues a PatchSpeakerProfile carrying the
// edited fields.
func TestPeopleScreenEditSavesPatch(t *testing.T) {
	fake := testutil.NewFakeClient()
	fake.Profiles = []notoapi.SpeakerProfile{{ID: "p1", DisplayName: "Alice"}}
	ctx := peopleCtx(fake)

	p := newPeopleScreen().(*peopleScreen)
	p.update(ctx, p.enter(ctx, "")())

	// e → edit, type a new pronoun on the second field, Enter → save.
	p.update(ctx, keyMsg("e"))
	if !p.editing {
		t.Fatal("expected edit mode after pressing e")
	}
	p.update(ctx, keyMsg("down")) // move to Pronouns
	for _, r := range "she/her" {
		p.update(ctx, keyMsg(string(r)))
	}
	_, cmd := p.update(ctx, keyMsg("enter"))
	if cmd == nil {
		t.Fatal("expected a save command on Enter")
	}
	cmd() // fire PatchSpeakerProfile against the fake

	got, _ := fake.GetSpeakerProfile(ctx.ctx, "p1")
	if got.Pronouns != "she/her" {
		t.Errorf("patched pronouns = %q, want she/her", got.Pronouns)
	}
}

// formToProfile groups affiliation cells and drops a row whose cells are all
// blank (the implicit "remove" gesture).
func TestPeopleFormToProfileDropsBlankAffiliations(t *testing.T) {
	p := &peopleScreen{detail: notoapi.SpeakerProfile{ID: "p1"}}
	p.buildForm(notoapi.SpeakerProfile{
		DisplayName: "Alice",
		Affiliations: []notoapi.Affiliation{
			{Context: "uni", Organization: "TU", Email: "a@tu.edu"},
			{}, // blank → must be dropped
		},
	})
	got := p.formToProfile()
	if got.DisplayName != "Alice" {
		t.Errorf("name = %q, want Alice", got.DisplayName)
	}
	if len(got.Affiliations) != 1 {
		t.Fatalf("kept %d affiliations, want 1 (blank dropped)", len(got.Affiliations))
	}
	if got.Affiliations[0].Context != "uni" || got.Affiliations[0].Email != "a@tu.edu" {
		t.Errorf("affiliation = %+v, want uni/TU/a@tu.edu", got.Affiliations[0])
	}
}

// On the Speakers tab, opening the picker (g) and pressing a digit assigns the
// ranked candidate: links the mapping (status manual) and relabels the
// transcript so the name shows here too.
func TestSpeakersTabAssignViaDigit(t *testing.T) {
	fake := testutil.NewFakeClient()
	ctx := peopleCtx(fake)
	d := newDetailPane()
	d.id_ = "m1"
	d.tab = tabSpeakers
	d.speakers = []speakerStat{{ID: "A", Name: "Speaker A"}}
	d.mappings = map[string]notoapi.MeetingSpeakerMapping{
		"A": {MeetingSpeakerID: "A", MatchStatus: "new", Candidates: []notoapi.SpeakerCandidate{
			{ProfileID: "p-bob", DisplayName: "Bob", Score: 0.82, Rank: 1},
		}},
	}

	if handled, _ := d.handleKey(ctx, keyMsg("g")); !handled || !d.assignOpen {
		t.Fatalf("expected assign picker open (handled=%v open=%v)", handled, d.assignOpen)
	}
	_, cmd := d.handleKey(ctx, keyMsg("1"))
	if cmd == nil {
		t.Fatal("expected an assign command")
	}
	cmd()

	if len(fake.PatchedMappings) != 1 {
		t.Fatalf("expected 1 mapping patch, got %d", len(fake.PatchedMappings))
	}
	entry := fake.PatchedMappings[0].Patch.Mappings[0]
	if entry.ProfileID == nil || *entry.ProfileID != "p-bob" {
		t.Errorf("assigned profile = %v, want p-bob", entry.ProfileID)
	}
	if entry.MatchStatus == nil || *entry.MatchStatus != "manual" {
		t.Errorf("status = %v, want manual", entry.MatchStatus)
	}
	if len(fake.RenamedSpeakers) != 1 || fake.RenamedSpeakers[0].DisplayName != "Bob" {
		t.Errorf("expected transcript relabel to Bob, got %+v", fake.RenamedSpeakers)
	}
}

// Naming an unresolved speaker (one with a mapping but no linked profile)
// creates a new person seeded from that meeting-speaker.
func TestSpeakersTabNewPersonCreatesProfile(t *testing.T) {
	fake := testutil.NewFakeClient()
	ctx := peopleCtx(fake)
	d := newDetailPane()
	d.id_ = "m1"
	d.tab = tabSpeakers
	d.speakers = []speakerStat{{ID: "A", Name: "Speaker A"}}
	d.mappings = map[string]notoapi.MeetingSpeakerMapping{
		"A": {MeetingSpeakerID: "A", MatchStatus: "new"},
	}

	if _, _ = d.handleKey(ctx, keyMsg("e")); !d.editorOpen {
		t.Fatal("expected editor open after e")
	}
	d.editorInput.SetValue("Carol")
	_, cmd := d.handleKey(ctx, keyMsg("enter"))
	if cmd == nil {
		t.Fatal("expected a create command")
	}
	cmd()

	if len(fake.CreatedProfiles) != 1 || fake.CreatedProfiles[0].DisplayName != "Carol" {
		t.Fatalf("expected CreateSpeakerProfile(Carol), got %+v", fake.CreatedProfiles)
	}
	if fake.CreatedProfiles[0].FromMeetingID != "m1" || fake.CreatedProfiles[0].FromSpeakerID != "A" {
		t.Errorf("expected FromMeeting m1/A, got %+v", fake.CreatedProfiles[0])
	}
}

// Tab focuses the detail's meetings list; Enter on a meeting jumps to it on
// the dashboard (switchScreenMsg with a "meeting:<id>" param) for quick ref nav.
func TestPeopleTabFocusesMeetingsAndJumps(t *testing.T) {
	seen := time.Date(2026, 5, 20, 9, 0, 0, 0, time.UTC)
	ctx := peopleCtx(testutil.NewFakeClient())
	p := newPeopleScreen().(*peopleScreen)
	p.loading = false
	p.profiles = []notoapi.SpeakerProfile{{ID: "p1", DisplayName: "Alice"}}
	p.detailID = "p1"
	p.detail = notoapi.SpeakerProfile{ID: "p1", DisplayName: "Alice", Meetings: []notoapi.ProfileMeetingRef{
		{MeetingID: "m1", Title: "Q2 Roadmap", CreatedAt: seen, MatchStatus: "auto"},
		{MeetingID: "m2", Title: "Hiring loop", CreatedAt: seen, MatchStatus: "manual"},
	}}

	p.update(ctx, keyMsg("tab"))
	if !p.focusMeetings {
		t.Fatal("expected focusMeetings after Tab")
	}
	p.update(ctx, keyMsg("down")) // second meeting
	_, cmd := p.update(ctx, keyMsg("enter"))
	if cmd == nil {
		t.Fatal("expected a jump command on Enter")
	}
	sw, ok := cmd().(switchScreenMsg)
	if !ok {
		t.Fatalf("expected switchScreenMsg, got a different message")
	}
	if sw.ID != sDashboard || sw.Param != "meeting:m2" {
		t.Errorf("jump = %v/%q, want %v/meeting:m2", sw.ID, sw.Param, sDashboard)
	}

	// Tab again hands focus back to the directory list.
	p.update(ctx, keyMsg("tab"))
	if p.focusMeetings {
		t.Error("expected Tab to release meetings focus")
	}
}

// Delete opens a confirm overlay (captures keys) instead of deleting outright;
// esc cancels, Enter fires the delete.
func TestPeopleDeleteConfirmFlow(t *testing.T) {
	fake := testutil.NewFakeClient()
	fake.Profiles = []notoapi.SpeakerProfile{{ID: "p1", DisplayName: "Alice"}}
	ctx := peopleCtx(fake)
	p := newPeopleScreen().(*peopleScreen)
	p.update(ctx, p.enter(ctx, "")())

	p.update(ctx, keyMsg("d"))
	if p.confirm != confirmDelete {
		t.Fatalf("expected confirmDelete after d, got %v", p.confirm)
	}
	if !p.inputActive() {
		t.Error("confirm overlay should make inputActive true so globals don't steal keys")
	}
	if out := ansi.Strip(p.view(ctx)); !strings.Contains(out, "Delete Alice?") {
		t.Errorf("expected confirm overlay prompt, got:\n%s", out)
	}

	p.update(ctx, keyMsg("esc"))
	if p.confirm != confirmNone {
		t.Fatal("esc should clear the confirm without deleting")
	}
	if len(fake.Profiles) != 1 {
		t.Fatalf("esc must not delete; have %d profiles", len(fake.Profiles))
	}

	p.update(ctx, keyMsg("d"))
	_, cmd := p.update(ctx, keyMsg("enter"))
	if cmd == nil {
		t.Fatal("expected a delete command on Enter")
	}
	cmd()
	if len(fake.Profiles) != 0 {
		t.Errorf("expected Alice deleted, %d profiles remain", len(fake.Profiles))
	}
}

// Merge picks a survivor then routes through the same confirm overlay before
// folding; confirming fires the merge (the source profile is removed).
func TestPeopleMergeConfirmFlow(t *testing.T) {
	fake := testutil.NewFakeClient()
	fake.Profiles = []notoapi.SpeakerProfile{
		{ID: "p1", DisplayName: "Alice"},
		{ID: "p2", DisplayName: "Bob"},
	}
	ctx := peopleCtx(fake)
	p := newPeopleScreen().(*peopleScreen)
	p.update(ctx, p.enter(ctx, "")())

	p.update(ctx, keyMsg("m"))
	if !p.merging || p.mergeIdx != 1 {
		t.Fatalf("expected merge mode with survivor idx 1, got merging=%v idx=%d", p.merging, p.mergeIdx)
	}
	// Enter on the survivor pick opens the confirm, it does not merge yet.
	p.update(ctx, keyMsg("enter"))
	if p.confirm != confirmMerge {
		t.Fatalf("expected confirmMerge after Enter, got %v", p.confirm)
	}
	if len(fake.Profiles) != 2 {
		t.Fatal("merge must not fire before confirmation")
	}
	if out := ansi.Strip(p.view(ctx)); !strings.Contains(out, "Merge into Bob?") {
		t.Errorf("expected merge confirm prompt naming the survivor, got:\n%s", out)
	}
	// Confirm → fold Alice into Bob.
	_, cmd := p.update(ctx, keyMsg("enter"))
	if cmd == nil {
		t.Fatal("expected a merge command on confirm")
	}
	cmd()
	if len(fake.Profiles) != 1 || fake.Profiles[0].ID != "p2" {
		t.Errorf("expected only survivor Bob (p2) to remain, got %+v", fake.Profiles)
	}
}

// The identify dialog (g) loads the full directory, filters it by the typed
// query, and Enter reassigns the speaker to the picked person — overwriting an
// existing link, so it's the correction path.
func TestAssignDialogSearchReassigns(t *testing.T) {
	fake := testutil.NewFakeClient()
	fake.Profiles = []notoapi.SpeakerProfile{
		{ID: "p-alice", DisplayName: "Alice Nguyen"},
		{ID: "p-bob", DisplayName: "Bob Martins"},
	}
	ctx := peopleCtx(fake)
	d := newDetailPane()
	d.id_ = "m1"
	d.tab = tabSpeakers
	d.speakers = []speakerStat{{ID: "A", Name: "Speaker A"}}
	cur := "p-alice"
	d.mappings = map[string]notoapi.MeetingSpeakerMapping{
		"A": {MeetingSpeakerID: "A", MatchStatus: "manual", ProfileID: &cur, ProfileName: "Alice Nguyen"},
	}

	handled, cmd := d.handleKey(ctx, keyMsg("g"))
	if !handled || !d.assignDialogOpen() {
		t.Fatalf("expected dialog open after g (handled=%v open=%v)", handled, d.assignDialogOpen())
	}
	if cmd == nil {
		t.Fatal("expected the dialog to request the people directory")
	}
	d.update(ctx, cmd()) // profilesLoadedMsg → directory
	if len(d.directory) != 2 {
		t.Fatalf("expected directory loaded (2), got %d", len(d.directory))
	}

	for _, r := range "bob" {
		d.handleKey(ctx, keyMsg(string(r)))
	}
	if rows := d.assignRows(); rows[0].name != "Bob Martins" {
		t.Fatalf("expected Bob first after filtering, got %q", rows[0].name)
	}
	_, cmd = d.handleKey(ctx, keyMsg("enter"))
	if cmd == nil {
		t.Fatal("expected an assign command")
	}
	cmd()
	if len(fake.PatchedMappings) != 1 {
		t.Fatalf("expected one mapping patch, got %d", len(fake.PatchedMappings))
	}
	entry := fake.PatchedMappings[0].Patch.Mappings[0]
	if entry.ProfileID == nil || *entry.ProfileID != "p-bob" {
		t.Errorf("reassigned profile = %v, want p-bob", entry.ProfileID)
	}
}

// The dialog's create-new row creates a person seeded from the speaker's
// voiceprint, named with the typed query.
func TestAssignDialogCreatesNewFromQuery(t *testing.T) {
	fake := testutil.NewFakeClient()
	ctx := peopleCtx(fake)
	d := newDetailPane()
	d.id_ = "m1"
	d.tab = tabSpeakers
	d.speakers = []speakerStat{{ID: "A", Name: "Speaker A"}}
	d.mappings = map[string]notoapi.MeetingSpeakerMapping{
		"A": {MeetingSpeakerID: "A", MatchStatus: "new"},
	}

	d.handleKey(ctx, keyMsg("g"))
	for _, r := range "Dakota" {
		d.handleKey(ctx, keyMsg(string(r)))
	}
	// No directory matches → only the create row remains, and it's selected.
	rows := d.assignRows()
	if len(rows) != 1 || !rows[len(rows)-1].create {
		t.Fatalf("expected just the create row, got %d rows", len(rows))
	}
	_, cmd := d.handleKey(ctx, keyMsg("enter"))
	if cmd == nil {
		t.Fatal("expected a create command")
	}
	cmd()
	if len(fake.CreatedProfiles) != 1 || fake.CreatedProfiles[0].DisplayName != "Dakota" {
		t.Fatalf("expected create Dakota, got %+v", fake.CreatedProfiles)
	}
	if fake.CreatedProfiles[0].FromSpeakerID != "A" {
		t.Errorf("expected seeded from speaker A, got %+v", fake.CreatedProfiles[0])
	}
}

// Enter on a linked speaker jumps to that person's People page (view mode),
// mirroring the People→meeting jump.
func TestSpeakersTabEnterOpensLinkedPerson(t *testing.T) {
	ctx := peopleCtx(testutil.NewFakeClient())
	d := newDetailPane()
	d.id_ = "m1"
	d.tab = tabSpeakers
	d.speakers = []speakerStat{{ID: "A", Name: "Speaker A"}}
	pid := "p-alice"
	d.mappings = map[string]notoapi.MeetingSpeakerMapping{
		"A": {MeetingSpeakerID: "A", MatchStatus: "auto", ProfileID: &pid, ProfileName: "Alice"},
	}

	handled, cmd := d.handleKey(ctx, keyMsg("enter"))
	if !handled || cmd == nil {
		t.Fatalf("expected Enter handled with a nav command (handled=%v cmd=%v)", handled, cmd)
	}
	sw, ok := cmd().(switchScreenMsg)
	if !ok {
		t.Fatalf("expected switchScreenMsg from Enter")
	}
	if sw.ID != sPeople || sw.Param != "person:p-alice" {
		t.Errorf("nav = %v/%q, want %v/person:p-alice", sw.ID, sw.Param, sPeople)
	}
}

// Enter on an unresolved speaker (mapping but no linked person) creates a person
// seeded from its voiceprint, then navigates to the People edit form to name it.
func TestSpeakersTabEnterCreatesAndOpensPerson(t *testing.T) {
	fake := testutil.NewFakeClient()
	ctx := peopleCtx(fake)
	d := newDetailPane()
	d.id_ = "m1"
	d.tab = tabSpeakers
	d.speakers = []speakerStat{{ID: "A", Name: "Speaker A"}}
	d.mappings = map[string]notoapi.MeetingSpeakerMapping{
		"A": {MeetingSpeakerID: "A", MatchStatus: "new"}, // mapping, no profile
	}

	handled, cmd := d.handleKey(ctx, keyMsg("enter"))
	if !handled || cmd == nil {
		t.Fatal("expected Enter handled with a create command")
	}
	msg := cmd() // fires CreateSpeakerProfile against the fake
	open, ok := msg.(speakerPersonOpenMsg)
	if !ok {
		t.Fatalf("expected speakerPersonOpenMsg, got a different message")
	}
	if open.Err != nil {
		t.Fatalf("create failed: %v", open.Err)
	}
	if !open.Edit {
		t.Error("expected Edit=true so the new person opens in the edit form")
	}
	if len(fake.CreatedProfiles) != 1 || fake.CreatedProfiles[0].FromSpeakerID != "A" {
		t.Fatalf("expected a profile created from speaker A, got %+v", fake.CreatedProfiles)
	}

	// The pane turns that result into a navigation to the People edit form.
	navCmd := d.update(ctx, open)
	if navCmd == nil {
		t.Fatal("expected a navigation command from the open message")
	}
	sw, ok := navCmd().(switchScreenMsg)
	if !ok || sw.ID != sPeople {
		t.Fatalf("expected nav to People screen")
	}
	if !strings.HasPrefix(sw.Param, "person-edit:") {
		t.Errorf("expected a person-edit param, got %q", sw.Param)
	}
}

// The People screen honors the jump params: "person:<id>" preselects in view
// mode; "person-edit:<id>" preselects and opens the edit form ready to rename.
func TestPeopleEnterPreselectsFromParam(t *testing.T) {
	fake := testutil.NewFakeClient()
	fake.Profiles = []notoapi.SpeakerProfile{
		{ID: "p1", DisplayName: "Alice"},
		{ID: "p2", DisplayName: "Bob"},
	}
	ctx := peopleCtx(fake)

	p := newPeopleScreen().(*peopleScreen)
	p.update(ctx, p.enter(ctx, "person:p2")())
	if p.cursor != 1 || p.detailID != "p2" {
		t.Fatalf("person:p2 should preselect Bob (cursor=%d id=%q)", p.cursor, p.detailID)
	}
	if p.editing {
		t.Error("person: (no edit) should not open the edit form")
	}

	p = newPeopleScreen().(*peopleScreen)
	p.update(ctx, p.enter(ctx, "person-edit:p2")())
	if p.cursor != 1 {
		t.Fatalf("person-edit:p2 should preselect Bob, cursor=%d", p.cursor)
	}
	if !p.editing {
		t.Fatal("person-edit: should open the edit form")
	}
	if got := p.formVals[0]; got != "Bob" {
		t.Errorf("edit form Name = %q, want Bob (ready to rename)", got)
	}
}

func keyMsg(s string) tea.KeyPressMsg {
	switch s {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	}
	return tea.KeyPressMsg{Code: []rune(s)[0], Text: s}
}
