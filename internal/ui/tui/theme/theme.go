// Package theme owns the lipgloss styles used by every screen.
//
// The palette is a small, shadcn / Tailwind-style token system rather than a
// scatter of ad-hoc hex codes. Three layers:
//
//   - a single NEUTRAL spine (Tailwind "Slate", nudged slightly bluer) that
//     supplies every surface and every shade of body/secondary/dim text, so
//     the grays never drift;
//   - a BRAND + SEMANTIC set — one blue hero accent plus fixed
//     success/warning/danger/info colors that only ever signal meaning;
//   - a reserved SPEAKER identity ramp that is deliberately kept clear of the
//     semantic hues, so a colored name can never read as a status.
//
// Designed to feel like a serious tool: restrained accent, strong borders,
// a generous gray hierarchy for secondary text. There is exactly one place a
// color is named — here.
package theme

import (
	"image/color"

	"charm.land/lipgloss/v2"
)

// Theme holds the color palette. In lipgloss v2, lipgloss.Color is a
// constructor func returning the stdlib color.Color interface (not a string
// type as in v1), so these palette fields hold color.Color values.
type Theme struct {
	// --- brand + semantic: these colors only ever mean something ---
	Primary       color.Color // hero blue — focus, selection, titles, key hints
	PrimaryBright color.Color // lighter accent — the click/press flash
	Secondary     color.Color // supporting cool — links, chips, questions
	Info          color.Color // in-progress / informational (transcribed, running)
	Success       color.Color // ok / done / decisions
	Warning       color.Color // attention / todo / risks
	Danger        color.Color // error / recording

	// --- neutral spine (Slate). Surfaces, dark→light. ---
	Background   color.Color // app canvas
	Surface      color.Color // raised body / cards (reserved for fills)
	Overlay      color.Color // modals: help, palette
	Border       color.Color // panel edges
	BorderSubtle color.Color // inner rules

	// --- neutral spine. Text, bright→faint. ---
	Bright     color.Color // focused input
	Foreground color.Color // body text
	Subtle     color.Color // subtitles, status bar
	Muted      color.Color // meta, rules, placeholders, dim
	Faint      color.Color // disabled / dimmed-background text

	OnAccent color.Color // text drawn on top of a bright accent fill

	// --- reserved speaker identity ramp (never used for status) ---
	SpeakerA color.Color
	SpeakerB color.Color
	SpeakerC color.Color
}

// Dark is the default theme. Built for terminals that respect truecolor;
// degrades gracefully where they don't. Every value is a Tailwind token so
// the scheme stays internally consistent and easy to reason about.
func Dark() Theme {
	return Theme{
		// brand + semantic
		Primary:       lipgloss.Color("#60a5fa"), // blue-400   — hero accent
		PrimaryBright: lipgloss.Color("#93c5fd"), // blue-300   — click/press flash
		Secondary:     lipgloss.Color("#818cf8"), // indigo-400 — supporting cool
		Info:          lipgloss.Color("#22d3ee"), // cyan-400   — in-progress / info
		Success:       lipgloss.Color("#34d399"), // emerald-400
		Warning:       lipgloss.Color("#fbbf24"), // amber-400
		Danger:        lipgloss.Color("#f87171"), // red-400

		// neutral surfaces (Slate, nudged bluer)
		Background:   lipgloss.Color("#0b1120"),
		Surface:      lipgloss.Color("#111b2e"),
		Overlay:      lipgloss.Color("#17243d"),
		Border:       lipgloss.Color("#1e293b"), // slate-800
		BorderSubtle: lipgloss.Color("#182338"),

		// neutral text ramp (bright → faint)
		Bright:     lipgloss.Color("#f8fafc"), // slate-50
		Foreground: lipgloss.Color("#e2e8f0"), // slate-200
		Subtle:     lipgloss.Color("#94a3b8"), // slate-400
		Muted:      lipgloss.Color("#64748b"), // slate-500
		Faint:      lipgloss.Color("#475569"), // slate-600

		OnAccent: lipgloss.Color("#0b1120"), // == Background

		// Speaker palette is deliberately outside the semantic hues so a
		// name in a summary line can't be confused with a category chip.
		// Order matters: the two most common speakers get the most
		// unambiguous hues (purple, pink); the cyan-adjacent teal is third
		// so it can't be mistaken for the cyan Info tone. Extras
		// (orange/violet/rose) live in speakers_helpers.go.
		SpeakerA: lipgloss.Color("#c084fc"), // purple-400
		SpeakerB: lipgloss.Color("#f472b6"), // pink-400
		SpeakerC: lipgloss.Color("#2dd4bf"), // teal-400
	}
}

type Styles struct {
	T               Theme
	Header          lipgloss.Style
	HeaderEm        lipgloss.Style
	Muted           lipgloss.Style
	Success         lipgloss.Style
	Warning         lipgloss.Style
	Danger          lipgloss.Style
	Info            lipgloss.Style
	Secondary       lipgloss.Style
	Title           lipgloss.Style
	Subtitle        lipgloss.Style
	Panel           lipgloss.Style
	PanelFocused    lipgloss.Style
	PanelTitle      lipgloss.Style
	Row             lipgloss.Style
	RowSelected     lipgloss.Style
	RowHover        lipgloss.Style
	RowHoverBg      lipgloss.Style
	RowPressed      lipgloss.Style
	LabelHover      lipgloss.Style
	LabelPressed    lipgloss.Style
	ScrollThumb     lipgloss.Style
	ScrollTrack     lipgloss.Style
	DividerHover    lipgloss.Style
	DividerActive   lipgloss.Style
	Badge           lipgloss.Style
	BadgeOK         lipgloss.Style
	BadgeWarn       lipgloss.Style
	BadgeDanger     lipgloss.Style
	BadgeInfo       lipgloss.Style
	StatusBar       lipgloss.Style
	HintBar         lipgloss.Style
	Recording       lipgloss.Style
	RecordingPulse  lipgloss.Style
	Chip            lipgloss.Style
	ChipKey         lipgloss.Style
	Hint            lipgloss.Style
	Input           lipgloss.Style
	InputFocused    lipgloss.Style
	OverlayBox      lipgloss.Style
	OverlayTitle    lipgloss.Style
	Decision        lipgloss.Style
	Action          lipgloss.Style
	Risk            lipgloss.Style
	Question        lipgloss.Style
	SpeakerA        lipgloss.Style
	SpeakerB        lipgloss.Style
	SpeakerC        lipgloss.Style
	Citation        lipgloss.Style
	MeterFilled     lipgloss.Style
	MeterEmpty      lipgloss.Style
	MeterClip       lipgloss.Style
	Highlight       lipgloss.Style
	HighlightActive lipgloss.Style
}

func NewStyles() Styles {
	t := Dark()
	return Styles{
		T:            t,
		Header:       lipgloss.NewStyle().Foreground(t.Primary).Bold(true),
		HeaderEm:     lipgloss.NewStyle().Foreground(t.Foreground).Bold(true),
		Muted:        lipgloss.NewStyle().Foreground(t.Muted),
		Success:      lipgloss.NewStyle().Foreground(t.Success),
		Warning:      lipgloss.NewStyle().Foreground(t.Warning),
		Danger:       lipgloss.NewStyle().Foreground(t.Danger),
		Info:         lipgloss.NewStyle().Foreground(t.Info),
		Secondary:    lipgloss.NewStyle().Foreground(t.Secondary),
		Title:        lipgloss.NewStyle().Foreground(t.Primary).Bold(true),
		Subtitle:     lipgloss.NewStyle().Foreground(t.Subtle),
		Panel:        lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(t.Border).Padding(0, 1),
		PanelFocused: lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(t.Primary).Padding(0, 1),
		PanelTitle:   lipgloss.NewStyle().Foreground(t.Primary).Bold(true),
		Row:          lipgloss.NewStyle().Foreground(t.Foreground),
		// --- FILL feedback family (full-width surfaces: nav pills, list rows) ---
		// These signal "selected" with a SOLID accent bar so the whole footprint
		// reads active at a glance (a dashboard meeting row, a config section, the
		// nav pill at the top all share this). One accent ramp across states:
		// selected = the calm Primary fill, the press flash = brighter
		// PrimaryBright, hover = a quiet Overlay raise. Same footprint in every
		// state, so a change only recolours — never resizes.
		RowSelected: lipgloss.NewStyle().Foreground(t.OnAccent).Background(t.Primary).Bold(true),
		RowHover:    lipgloss.NewStyle().Foreground(t.Bright).Background(t.Overlay).Bold(true),
		// Background-only twin of RowHover, for full-width body rows: the hover
		// fill is laid UNDER an already-coloured, multi-segment line (paintRow),
		// so it must not set a foreground — each segment keeps its own colour.
		RowHoverBg: lipgloss.NewStyle().Background(t.Overlay),
		// Pressed is shown only while you HOLD a click on the already-focused
		// element (re-clicking what you're on) — a brighter accent than the
		// selected fill, so you can see the repeat-click register. A click on a
		// different element doesn't use this; its focus change is the feedback.
		RowPressed: lipgloss.NewStyle().Foreground(t.OnAccent).Background(t.PrimaryBright).Bold(true),
		// --- LABEL feedback family (inline text: detail tabs, action chips) ---
		// These show "active" as a recoloured GLYPH (HeaderEm — bold body text),
		// NOT a fill, so a solid accent bar would be far too loud beside one.
		// Feedback stays purely in the FOREGROUND — no background fill at all:
		// hover brightens the text, the press flash shifts it to the accent. So
		// hovering or re-clicking a tab reads as "you hit it again" via the font
		// colour alone, never a bar or a box behind the word. Tune the label
		// language here without touching the fill family above.
		LabelHover:   lipgloss.NewStyle().Foreground(t.Bright).Bold(true),
		LabelPressed: lipgloss.NewStyle().Foreground(t.PrimaryBright).Bold(true),
		// Scrollbar gutter. Painted as a cell BACKGROUND (a space), NOT a
		// foreground █ glyph: a background fill covers the whole cell including
		// the inter-line leading, so a vertical run is gapless in EVERY terminal
		// — macOS Terminal.app shows gaps between stacked █ glyphs because it
		// doesn't redraw block elements to fill the cell the way Ghostty/iTerm2
		// do, but it fills cell backgrounds seamlessly (same as the selection
		// bars). The thumb is a NEUTRAL gray — deliberately NOT the Primary blue,
		// so a scroll position never reads as the selection/focus accent. The
		// track is barely-there so the bar only asserts itself where content is.
		// Both are a single column wide.
		ScrollThumb: lipgloss.NewStyle().Background(t.Subtle),
		ScrollTrack: lipgloss.NewStyle().Background(t.BorderSubtle),
		// Resizable pane divider. Invisible at rest (the seam is blank); on
		// hover it shows a neutral rule the colour of the panel edges, so it
		// reads as a grabbable border; while dragging it glows the bright
		// accent so the resize plainly registers.
		DividerHover:   lipgloss.NewStyle().Foreground(t.Border),
		DividerActive:  lipgloss.NewStyle().Foreground(t.PrimaryBright).Bold(true),
		Badge:          lipgloss.NewStyle().Padding(0, 1),
		BadgeOK:        lipgloss.NewStyle().Foreground(t.Success).Bold(true),
		BadgeWarn:      lipgloss.NewStyle().Foreground(t.Warning).Bold(true),
		BadgeDanger:    lipgloss.NewStyle().Foreground(t.Danger).Bold(true),
		BadgeInfo:      lipgloss.NewStyle().Foreground(t.Info).Bold(true),
		StatusBar:      lipgloss.NewStyle().Foreground(t.Subtle),
		HintBar:        lipgloss.NewStyle().Foreground(t.Muted),
		Recording:      lipgloss.NewStyle().Foreground(t.Danger).Bold(true),
		RecordingPulse: lipgloss.NewStyle().Foreground(t.Danger).Bold(true),
		Chip:           lipgloss.NewStyle().Foreground(t.Secondary),
		ChipKey:        lipgloss.NewStyle().Foreground(t.Primary).Bold(true),
		Hint:           lipgloss.NewStyle().Foreground(t.Muted),
		Input:          lipgloss.NewStyle().Foreground(t.Foreground),
		InputFocused:   lipgloss.NewStyle().Foreground(t.Bright).Bold(true),
		OverlayBox: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(t.Primary).
			Background(t.Overlay).
			Padding(1, 2),
		OverlayTitle: lipgloss.NewStyle().Foreground(t.Primary).Bold(true),
		// Summary categories share one mapping everywhere they appear (the
		// detail tabs and the dashboard/header counter strips), each on a
		// distinct, semantically apt hue: decisions = agreed (green),
		// actions = the brand "do" verb (blue), risks = attention (amber),
		// questions = open/unresolved (indigo).
		Decision:    lipgloss.NewStyle().Foreground(t.Success),
		Action:      lipgloss.NewStyle().Foreground(t.Primary),
		Risk:        lipgloss.NewStyle().Foreground(t.Warning),
		Question:    lipgloss.NewStyle().Foreground(t.Secondary),
		SpeakerA:    lipgloss.NewStyle().Foreground(t.SpeakerA).Bold(true),
		SpeakerB:    lipgloss.NewStyle().Foreground(t.SpeakerB).Bold(true),
		SpeakerC:    lipgloss.NewStyle().Foreground(t.SpeakerC).Bold(true),
		Citation:    lipgloss.NewStyle().Foreground(t.Secondary),
		MeterFilled: lipgloss.NewStyle().Foreground(t.Success),
		MeterEmpty:  lipgloss.NewStyle().Foreground(t.Muted),
		MeterClip:   lipgloss.NewStyle().Foreground(t.Danger).Bold(true),
		// FTS hit highlight: amber background, dark fg. The "active"
		// variant marks the segment `n`/`N` is currently parked on with the
		// brand fill so the user can tell where they jumped to.
		Highlight:       lipgloss.NewStyle().Foreground(t.OnAccent).Background(t.Warning).Bold(true),
		HighlightActive: lipgloss.NewStyle().Foreground(t.OnAccent).Background(t.Primary).Bold(true),
	}
}
