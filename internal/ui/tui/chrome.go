package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/lukasstrickler/noto/internal/ui/tui/hit"
	"github.com/lukasstrickler/noto/internal/ui/tui/theme"
)

// chrome.go owns the persistent frame around every screen: the top nav
// bar (where you are / where you can go / what needs attention), the
// bottom system status bar, the global hint row, and the transient
// banner. Keeping them here keeps root.go focused on the router.

// --- top nav bar -------------------------------------------------------
//
// The header is the wayfinding bar. It shows the logo, the numbered
// top-level pages as a pill strip (active page filled with the accent so
// a newcomer can see at a glance both where they are and where they can
// go), an optional breadcrumb tail for a contextual sub-screen, and — on
// each page that needs a human — an amber ⚑N flag right on its pill, so
// attention points at the screen that owns the work instead of a generic
// badge off in the corner. Recording/idle, meeting count, index and jobs
// live in the bottom status bar so the top stays navigation + attention.

// navTier is the rendering fidelity of the nav pill strip, decided ONCE for the
// whole strip so every pill compresses in lock-step (never a ragged mix). It is
// the three-step responsive family the user sees everywhere: full label, a
// distributed short form (trailing letters dropped), and the digit alone.
type navTier int

const (
	navFull navTier = iota // ` digit title ` — the whole label
	navMid                 // ` digit shrt `  — title shortened to a shared budget
	navMin                 // ` digit `       — digit only, the narrowest readable form
)

func (m *rootModel) renderHeader() string {
	s := m.styles
	logoPart := s.Header.Render("◉ noto") + "  "
	originX := lipgloss.Width(logoPart)
	// Pick the widest tier whose rendered strip fits, so a narrow terminal steps
	// down full → mid → digit-only instead of spilling past the edge. Measured,
	// not guessed: render each tier with a nil hit map (it registers nothing) and
	// take the first that fits, then draw the chosen tier into the frame's map so
	// only the pills actually shown are clickable.
	avail := m.width - originX - 1
	tier := navMin
	for _, t := range []navTier{navFull, navMid, navMin} {
		if lipgloss.Width(m.renderNavStrip(nil, originX, t)) <= avail {
			tier = t
			break
		}
	}
	return logoPart + m.renderNavStrip(m.frameHits, originX, tier)
}

// navMidBudget is the shared per-title cell budget for the navMid tier: the space
// left after the ACTIVE pill's full label and every pill's fixed chrome + gaps,
// divided EQUALLY across the OTHER pills so they all drop their trailing letters
// together ("distributed"), never one label long while its neighbour is clipped
// to nothing. The active pill is reserved at full width because navPill always
// shows it spelled out. Flags (⚑N) are rare and ignored here; the render-to-
// measure step in renderHeader still falls through to navMin if a flagged mid
// strip overflows, so the budget only needs to be close.
func (m *rootModel) navMidBudget(originX int) int {
	n := len(topScreens)
	if n <= 1 {
		return 0
	}
	const perPillFixed = 4 // " " + digit + " " + … + " " (1-char digit)
	const gap = 2          // cells between pills
	active := m.activeTopID()
	rest := m.width - originX - 1 - gap*(n-1)
	inactive := 0
	for i, sc := range topScreens {
		if sc.id == active {
			rest -= len(" " + navDigit(sc, i) + " " + sc.title + " ")
			continue
		}
		rest -= perPillFixed
		inactive++
	}
	if inactive == 0 {
		return 0
	}
	b := rest / inactive
	if b < 1 {
		b = 1
	}
	return b
}

// renderNavStrip renders the numbered top-level pages as a row of pills.
// Digits and labels are derived from topScreens/navBinding so the strip
// can never drift from the real nav keys. Every pill reserves the same
// ` digit title ` footprint whether or not it's active, so moving the
// selection only RECOLORS a pill — it never resizes it, and the strip
// cannot jitter as you move between pages (the bug the old strip had:
// the active pill was 2 cells wider than the inactive ones).
// renderNavStrip renders the pill row starting at column originX on row 0 of
// the frame, registering each pill as a clickable button in hits (which may
// be nil for a measure-only pass). Each pill is one button declaring its
// per-state look (navPill) and click action (navClick); place() resolves
// hover/selection and tracks the column, so a click or hover maps back to the
// right screen with no offset math here.
func (m *rootModel) renderNavStrip(hits *hit.Map[region], originX int, tier navTier) string {
	active := m.activeTopID()
	midBudget := 0
	if tier == navMid {
		midBudget = m.navMidBudget(originX)
	}
	row := hit.NewRow(hits, originX, 0)
	for i, sc := range topScreens {
		if i > 0 {
			row.Add("  ") // 2-cell gap between pills (not clickable)
		}
		sc, digit := sc, navDigit(sc, i)
		button{
			id:      navHitID(sc.id),
			active:  sc.id == active,
			onClick: m.navClick(sc.id),
			render: func(st uiState) string {
				return m.navPill(m.styles, sc, digit, st, tier, midBudget)
			},
		}.place(row, m.pointer())
	}
	return row.String()
}

// navTitle is the title a pill shows at the given tier: the full label, the
// distributed short form (trailing letters dropped to midBudget), or nothing at
// the digit-only tier.
func navTitle(title string, tier navTier, midBudget int) string {
	switch tier {
	case navMid:
		return shortenTo(title, midBudget)
	case navMin:
		return ""
	default:
		return title
	}
}

// navHitID is the stable hover/click identity of a nav pill.
func navHitID(id screenID) string { return "nav:" + string(id) }

// navDigit is the bare nav digit for screen i (navBinding's help key can read
// "2/p"; the strip shows just the digit).
func navDigit(sc screenReg, i int) string {
	d := sc.navBinding(i).Help().Key
	if idx := strings.IndexByte(d, '/'); idx >= 0 {
		d = d[:idx]
	}
	return d
}

// navClick switches to the screen behind a clicked nav pill. It mirrors the
// number-key handler exactly: clicking the page you're already on is a no-op
// (so it can't clobber a half-typed search), otherwise it switches.
func (m *rootModel) navClick(id screenID) clickAction {
	return func() tea.Cmd {
		if m.top().id() == id {
			return nil
		}
		return pushOrReplaceTo(id, "")
	}
}

// navPill renders one top-level page. The active page is the accent-filled
// pill; an inactive page reserves the SAME columns (` digit title `), just
// uncolored, so the strip stays positionally stable across selection. An amber
// ⚑N rides after the pill when the page has work (screenAttention) — the same
// token as the detail tab bar so a flag means the same thing everywhere. A
// breadcrumb tail (› agent) follows the active pill when a sub-screen is on top.
func (m *rootModel) navPill(s theme.Styles, sc screenReg, digit string, st uiState, tier navTier, midBudget int) string {
	flag := ""
	if n := screenAttention(m.statusBar, sc.id); n > 0 {
		flag = " " + attnCount(s, n)
	}
	// The ACTIVE page always shows its FULL label — you should always be able to
	// read where you are, no matter how tight the strip — so the fill path uses
	// the full title. The TIER governs only the OTHER pills: full title, a
	// distributed short form (trailing letters dropped to midBudget), or the digit
	// alone. Each pill keeps the same footprint across pointer states at a given
	// tier, so hovering/selecting only recolours and the strip never jitters.
	fullLabel := " " + digit + " " + sc.title + " "
	title := navTitle(sc.title, tier, midBudget)
	tierLabel := " " + digit + " "
	if title != "" {
		tierLabel += title + " "
	}
	// filled renders the pill as a solid bar (selected or the pressed flash). The
	// breadcrumb tail follows the active top pill when a sub-screen is pushed,
	// independent of the fill — so a press flash on the active pill keeps its tail
	// and the row never changes width mid-flash.
	filled := func(style lipgloss.Style) string {
		core := style.Render(fullLabel)
		if m.activeTopID() == sc.id && len(m.stack) > 1 {
			core += s.Muted.Render(" › ") + s.HeaderEm.Render(m.top().title())
		}
		return core + flag
	}
	switch st {
	case uiPressed:
		return filled(s.RowPressed)
	case uiActive:
		return filled(s.RowSelected)
	case uiHover:
		return s.RowHover.Render(tierLabel) + flag
	default: // uiNormal
		if title == "" {
			// Digit-only tier: a centered digit carries the whole pill.
			return s.ChipKey.Render(tierLabel) + flag
		}
		return " " + s.ChipKey.Render(digit) + " " + s.Muted.Render(title) + " " + flag
	}
}

// activeTopID is the id of the current top-level page (stack[0]); pushed
// sub-screens don't change which nav pill is lit.
func (m *rootModel) activeTopID() screenID {
	if len(m.stack) == 0 {
		return sDashboard
	}
	return m.stack[0].id()
}

// --- bottom status bar -------------------------------------------------

func (m *rootModel) renderStatusBar() string {
	s := m.styles
	parts := []string{}
	if m.recordingActive {
		parts = append(parts, s.Recording.Render("● REC")+" "+s.HeaderEm.Render(formatDuration(m.recordingElapsed)))
	} else {
		parts = append(parts, s.Muted.Render("○ idle"))
	}
	parts = append(parts, s.Muted.Render(fmt.Sprintf("⦿ %d meetings", m.statusBar.MeetingCount)))
	idxLabel := def(m.statusBar.IndexState, "clean")
	switch idxLabel {
	case "clean":
		parts = append(parts, s.Success.Render("✓ index "+idxLabel))
	case "indexing":
		parts = append(parts, s.Info.Render("⟳ index "+idxLabel))
	default:
		parts = append(parts, s.Warning.Render("△ index "+idxLabel))
	}
	switch {
	case m.statusBar.JobsRunning > 0:
		parts = append(parts, s.Info.Render(fmt.Sprintf("⚙ %d running", m.statusBar.JobsRunning)))
	case m.statusBar.JobsQueued > 0:
		parts = append(parts, s.Muted.Render(fmt.Sprintf("⌛ %d queued", m.statusBar.JobsQueued)))
	default:
		parts = append(parts, s.Muted.Render("⚙ jobs idle"))
	}
	left := strings.Join(parts, s.Muted.Render("  ·  "))
	return s.StatusBar.Render(left)
}

// --- global hint row ---------------------------------------------------

// renderHintBar draws the universal mode chips (palette / search / help / back
// / quit) that every screen shares — each a clickable button that replays its
// key, so the bar is as usable by mouse as by keyboard. hits/originY place the
// click regions on the bar's actual frame row (nil hits = a measure-only or
// banner-replaced pass that registers nothing). Screen-local actions stay in
// each screen's own body.
func (m *rootModel) renderHintBar(hits *hit.Map[region], originY int) string {
	bindings := []key.Binding{
		m.keys.Palette, m.keys.Search, m.keys.Help, m.keys.Back, m.keys.Quit,
	}
	row := hit.NewRow(hits, 0, originY)
	for i, b := range bindings {
		if i > 0 {
			row.Add("   ")
		}
		chipButton(row, m.pointer(), m.styles, fmt.Sprintf("hint:%d", i), b)
	}
	return m.styles.HintBar.Render(row.String())
}

func (m *rootModel) renderBanner() string {
	if m.banner == nil {
		return ""
	}
	s := m.styles
	var style = s.BadgeInfo
	switch m.banner.Kind {
	case "warn":
		style = s.BadgeWarn
	case "error":
		style = s.BadgeDanger
	}
	return style.Render(m.banner.Text)
}

// --- chip helpers ------------------------------------------------------

func hint(s theme.Styles, k, label string) string {
	return s.ChipKey.Render(k) + " " + s.Hint.Render(label)
}

// chip renders a key binding as a hint using the binding's OWN help text
// (key + description). This is the single path the UI uses to show a key,
// so the label can never drift from what's actually bound — change the
// key in keys.New() and every chip follows.
func chip(s theme.Styles, b key.Binding) string {
	h := b.Help()
	return hint(s, h.Key, h.Desc)
}

// chipAs renders a binding's key with a context-specific label, for spots
// where the generic description doesn't fit (e.g. Tab means "focus
// details" on the dashboard but "switch pane" in config).
func chipAs(s theme.Styles, b key.Binding, label string) string {
	return hint(s, b.Help().Key, label)
}

// chipPair renders two bindings as one "a/b label" chip, for paired
// movement keys (↑/↓, ←/→) shown as a single hint.
func chipPair(s theme.Styles, a, b key.Binding, label string) string {
	return hint(s, a.Help().Key+"/"+b.Help().Key, label)
}

// --- small formatters --------------------------------------------------

func formatDuration(seconds int) string {
	h := seconds / 3600
	rem := seconds % 3600
	mn := rem / 60
	sc := rem % 60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, mn, sc)
	}
	return fmt.Sprintf("%02d:%02d", mn, sc)
}

func def(s, d string) string {
	if s == "" {
		return d
	}
	return s
}
