package tui

import (
	"fmt"

	"github.com/lukasstrickler/noto/internal/transport/notoapi"
	"github.com/lukasstrickler/noto/internal/ui/tui/theme"
)

// attention.go is the ONE place the "something here needs you" visual language
// lives, so every level of the UI flags work the same way and a single flag can
// be traced from the nav strip all the way down to the exact item:
//
//	nav pill     dashboard ⚑N / people ⚑N / config ⚑N  (which SCREEN — chrome.go)
//	dashboard    ▍ bar + ⚑N cluster        (which meeting — dashboard_view.go)
//	tab bar      speakers ⚑N              (which tab — detail_pane_view.go)
//	speaker row  ▍ bar + ⚑ flag           (which speaker — detail_pane_speakers_view.go)
//
// Two tokens carry it everywhere:
//
//	⚑  the action flag (amber) — a count on aggregates, a verb on the item
//	▍  the row bar (amber) — runs down a flagged row and stays visible NEXT TO
//	   the blue cursor when that row is selected, so it never hides under the
//	   selection highlight (the whole point: attention must not disappear when
//	   you move onto it).
//
// The flag routes attention to the screen that OWNS the work (screenAttention),
// and within Identity, actionForStatus maps a meeting-speaker's match_status to
// the action the user should take.

// screenAttention reports how many items on a top-level screen need the user,
// given the latest status bar. This is the ONE place the nav strip's per-pill
// flags are derived, so a flag always points at the screen that owns the work
// rather than a single global badge in the corner:
//
//	dashboard  speakers still to identify    (status_bar.SpeakersToID)
//	people     provisional people to review  (status_bar.PeopleToReview)
//	config     a routed provider with no key (status_bar.ConfigIssues)
//
// Screens with no attention source return 0 and render no flag, so a fully
// triaged run shows a clean strip.
func screenAttention(sb notoapi.StatusBar, id screenID) int {
	switch id {
	case sDashboard:
		return sb.SpeakersToID
	case sPeople:
		return sb.PeopleToReview
	case sConfig:
		return sb.ConfigIssues
	default:
		return 0
	}
}

// actionForStatus returns the verb for what to do about a speaker mapping and
// whether it needs attention at all. Resolved (auto/manual) needs nothing.
func actionForStatus(status string) (verb string, needs bool) {
	switch status {
	case "auto", "manual":
		return "", false
	case "pending":
		return "confirm", true // a strong candidate awaits one-tap confirm
	case "new":
		return "review", true // auto-created provisional, not yet checked
	default: // "unmatched", ""
		return "identify", true // no match — search or create
	}
}

// attnCount renders the amber "⚑N" aggregate flag — a count of items needing
// the user on some container. It's the ONE token shared by the nav pills, the
// detail tab bar, and the config section nav, so a count flag looks and means
// the same wherever it appears in the trail. Callers own the surrounding space.
func attnCount(s theme.Styles, n int) string {
	return s.BadgeWarn.Render(fmt.Sprintf("⚑%d", n))
}

// attnGutter builds a list row's 3-cell left gutter so the cursor and the
// attention bar COEXIST: the blue cursor when selected, the amber bar when
// flagged, and BOTH (bar + cursor) when a flagged row is selected — the flag
// never vanishes under the selection highlight. Always exactly 3 visible cells
// so columns stay aligned across every row state.
func attnGutter(s theme.Styles, selected, attention bool) string {
	switch {
	case selected && attention:
		return s.Warning.Render("▍") + s.RowSelected.Render("▸ ")
	case selected:
		return s.RowSelected.Render(" ▸ ")
	case attention:
		return s.Warning.Render("▍") + "  "
	default:
		return "   "
	}
}
