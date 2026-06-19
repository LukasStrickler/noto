package tui

import (
	"image/color"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/lukasstrickler/noto/internal/ui/tui/theme"
)

// help_overlay.go owns the full-keymap help panel and the generic
// centered-overlay compositor it (and the confirm/identify dialogs) share.

// overlaySurface is the backed-style kit every modal (help, confirm, identify)
// draws with. lipgloss resets a span's background to the terminal default at the
// span's end, so any cell inside the rounded box that ISN'T explicitly backed —
// including the plain spaces between chips — punches a black hole through the
// surface. Every overlay used to re-derive these `s.X.Background(Overlay)` styles
// by hand and warn in a comment to "back every span"; this makes the rule
// structural — reach for a kit field and a hole is impossible.
type overlaySurface struct {
	BG      lipgloss.Style // bare surface fill — spacers, indents
	Head    lipgloss.Style // the modal's main title
	Section lipgloss.Style // a group heading inside the modal
	Title   lipgloss.Style // a dialog title (delete/merge/identify)
	Body    lipgloss.Style // body / row text
	Muted   lipgloss.Style // hints, secondary text
	Key     lipgloss.Style // key-glyph chips
}

// newOverlaySurface backs the shared style family with the theme's Overlay color.
func newOverlaySurface(s theme.Styles) overlaySurface {
	surf := s.T.Overlay
	back := func(st lipgloss.Style) lipgloss.Style { return st.Background(surf) }
	return overlaySurface{
		BG:      lipgloss.NewStyle().Background(surf),
		Head:    back(s.HeaderEm),
		Section: back(s.PanelTitle),
		Title:   back(s.OverlayTitle),
		Body:    back(s.Row),
		Muted:   back(s.Muted),
		Key:     back(s.ChipKey),
	}
}

// renderHelpOverlay is built entirely from the central key map: every
// key shown is read from its binding via chip(), so this overlay can
// never disagree with what the screens actually handle.
func (m *rootModel) renderHelpOverlay(under string) string {
	s := m.styles
	k := m.keys

	// The shared backed-style kit keeps the whole panel one continuous surface —
	// every span carries the Overlay background, so the spaces between chips don't
	// punch black holes through it (see overlaySurface).
	o := newOverlaySurface(s)
	bg, keyS, descS := o.BG, o.Key, o.Body
	titleS, headS, mutedS := o.Section, o.Head, o.Muted

	sep := bg.Render("   ")
	gap := bg.Render(" ")
	row := func(chips ...string) string { return bg.Render("  ") + strings.Join(chips, sep) }
	hc := func(b key.Binding) string {
		h := b.Help()
		return keyS.Render(h.Key) + gap + descS.Render(h.Desc)
	}
	hcAs := func(b key.Binding, label string) string {
		return keyS.Render(b.Help().Key) + gap + descS.Render(label)
	}
	hcPair := func(a, b key.Binding, label string) string {
		return keyS.Render(a.Help().Key+"/"+b.Help().Key) + gap + descS.Render(label)
	}

	navChips := make([]string, 0, len(topScreens))
	for _, b := range screenNavBindings() {
		navChips = append(navChips, hc(b))
	}

	lines := []string{
		headS.Render("noto — keys"),
		"",
		titleS.Render("Global"),
		row(navChips...),
		row(hc(k.Search), hc(k.Palette), hc(k.Help), hc(k.Back), hc(k.Quit)),
		row(hcPair(k.SidebarNarrower, k.SidebarWider, "resize sidebar"), hc(k.SidebarReset)),
		"",
		titleS.Render("Meetings list"),
		row(hcPair(k.Up, k.Down, "select"), hcAs(k.Enter, "focus details"), hcAs(k.Tab, "focus details")),
		row(hc(k.OpenAgent), hc(k.Delete), hc(k.ClearSearch)),
		"",
		titleS.Render("Search (/ focused)"),
		row(hcAs(k.Tab, "next match"), hcAs(k.ShiftTab, "prev match"), hcAs(k.Enter, "open at hit"), hcAs(k.Back, "to list")),
		"",
		titleS.Render("Details pane"),
		row(hcPair(k.TabPrev, k.TabNext, "switch tab"), hc(k.Transcript), hc(k.Speakers), hc(k.OpenAgent)),
		row(hcPair(k.Up, k.Down, "scroll/select"), hc(k.NextMatch), hc(k.PrevMatch), hcAs(k.Edit, "rename speaker")),
		row(hcAs(k.Assign, "identify / reassign"), hcAs(k.Enter, "open person page"), hcAs(k.Edit, "rename")),
		"",
		titleS.Render("Recorder"),
		row(hc(k.EditTitle), hc(k.Record), hc(k.Stop), hc(k.Marker)),
		"",
		titleS.Render("Config"),
		row(hcAs(k.Tab, "switch pane"), hcPair(k.Up, k.Down, "move"), hcAs(k.Enter, "set route/key"), hc(k.Test), hc(k.Remove)),
		"",
		titleS.Render("People"),
		row(hcPair(k.Up, k.Down, "select"), hc(k.Edit), hc(k.Delete), hc(k.Merge)),
		row(hcAs(k.Tab, "meetings"), hcAs(k.Enter, "open meeting")),
		"",
		titleS.Render("Attention legend"),
		row(mutedS.Render("nav ⚑N flags the page that needs you:")),
		row(mutedS.Render("  dashboard identify speakers · people review · config add a key")),
		row(mutedS.Render("dashboard ▍ row needs work · ⚑N speakers to identify · ✓ people all set")),
		"",
		mutedS.Render("press ? or esc to close"),
	}
	box := s.OverlayBox.Width(min(m.width-6, 78)).Render(strings.Join(lines, "\n"))
	return overlayCenter(under, box, m.width, m.height, s.T.Faint)
}

// overlayCenter composites a pre-styled box centered over a
// terminal-sized view. The background is dimmed (ANSI-stripped, then
// re-rendered faint) so the box reads as the foreground; the box keeps
// its own border/colors.
//
// lipgloss v2 does the compositing: a Compositor positions the box layer
// at (left, top) on top of the full-size background layer, drawn onto an
// explicit w×h Canvas. (Canvas.Compose alone ignores a layer's X/Y — only
// a Compositor applies per-layer offsets — so the box goes through one.)
func overlayCenter(under, box string, w, h int, dim color.Color) string {
	bw := lipgloss.Width(box)
	bh := lipgloss.Height(box)
	if bw > w {
		bw = w
	}
	if bh > h {
		bh = h
	}
	left := max(0, (w-bw)/2)
	top := max(0, (h-bh)/2)

	dimStyle := lipgloss.NewStyle().Faint(true).Foreground(dim)
	dimmed := dimStyle.Render(ansi.Strip(under))

	return lipgloss.NewCanvas(w, h).
		Compose(lipgloss.NewCompositor(
			lipgloss.NewLayer(dimmed),                  // z=0: dimmed background
			lipgloss.NewLayer(box).X(left).Y(top).Z(1), // z=1: centered box on top
		)).
		Render()
}
