// Package theme owns the lipgloss styles used by every screen.
// Designed to feel like a serious tool, not decoration: restrained
// color, strong borders, dim secondary text.
package theme

import "github.com/charmbracelet/lipgloss"

type Theme struct {
	Primary    lipgloss.Color
	Secondary  lipgloss.Color
	Muted      lipgloss.Color
	Background lipgloss.Color
	Surface    lipgloss.Color
	Danger     lipgloss.Color
	Warning    lipgloss.Color
	Success    lipgloss.Color
	Info       lipgloss.Color

	SpeakerA lipgloss.Color
	SpeakerB lipgloss.Color
	SpeakerC lipgloss.Color
}

// Dark is the default theme. Built for terminals that respect
// adaptive colors gracefully but degrade well on truecolor-less ones.
// Palette aims for a serious-tool look: restrained accent, strong
// dark surface, generous gray hierarchy for secondary text.
func Dark() Theme {
	return Theme{
		Primary:    lipgloss.Color("#22d3ee"), // soft cyan — main accent
		Secondary:  lipgloss.Color("#a78bfa"), // lavender — secondary text/links
		Muted:      lipgloss.Color("#64748b"), // slate-500
		Background: lipgloss.Color("#0b0f17"),
		Surface:    lipgloss.Color("#0f1623"),
		Danger:     lipgloss.Color("#f87171"),
		Warning:    lipgloss.Color("#fbbf24"),
		Success:    lipgloss.Color("#34d399"),
		Info:       lipgloss.Color("#60a5fa"),
		// Speaker palette is intentionally distinct from
		// decisions/actions/risks/questions accents so a name in a
		// summary line can't be confused with a category chip.
		// Picks: purple, teal, pink — vivid identity hues.
		SpeakerA: lipgloss.Color("#c084fc"), // purple-400
		SpeakerB: lipgloss.Color("#2dd4bf"), // teal-400
		SpeakerC: lipgloss.Color("#f472b6"), // pink-400
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
	bodyFG := lipgloss.Color("#e2e8f0") // slate-200
	dimFG := lipgloss.Color("#94a3b8")  // slate-400
	border := lipgloss.Color("#1e293b") // slate-800
	return Styles{
		T:              t,
		Header:         lipgloss.NewStyle().Foreground(t.Primary).Bold(true),
		HeaderEm:       lipgloss.NewStyle().Foreground(bodyFG).Bold(true),
		Muted:          lipgloss.NewStyle().Foreground(t.Muted),
		Success:        lipgloss.NewStyle().Foreground(t.Success),
		Warning:        lipgloss.NewStyle().Foreground(t.Warning),
		Danger:         lipgloss.NewStyle().Foreground(t.Danger),
		Info:           lipgloss.NewStyle().Foreground(t.Info),
		Secondary:      lipgloss.NewStyle().Foreground(t.Secondary),
		Title:          lipgloss.NewStyle().Foreground(t.Primary).Bold(true),
		Subtitle:       lipgloss.NewStyle().Foreground(dimFG),
		Panel:          lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(border).Padding(0, 1),
		PanelFocused:   lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(t.Primary).Padding(0, 1),
		PanelTitle:     lipgloss.NewStyle().Foreground(t.Primary).Bold(true),
		Row:            lipgloss.NewStyle().Foreground(bodyFG),
		RowSelected:    lipgloss.NewStyle().Foreground(lipgloss.Color("#0b0f17")).Background(t.Primary).Bold(true),
		Badge:          lipgloss.NewStyle().Padding(0, 1),
		BadgeOK:        lipgloss.NewStyle().Foreground(t.Success).Bold(true),
		BadgeWarn:      lipgloss.NewStyle().Foreground(t.Warning).Bold(true),
		BadgeDanger:    lipgloss.NewStyle().Foreground(t.Danger).Bold(true),
		BadgeInfo:      lipgloss.NewStyle().Foreground(t.Info).Bold(true),
		StatusBar:      lipgloss.NewStyle().Foreground(dimFG),
		HintBar:        lipgloss.NewStyle().Foreground(t.Muted),
		Recording:      lipgloss.NewStyle().Foreground(t.Danger).Bold(true),
		RecordingPulse: lipgloss.NewStyle().Foreground(t.Danger).Bold(true),
		Chip:           lipgloss.NewStyle().Foreground(t.Secondary),
		ChipKey:        lipgloss.NewStyle().Foreground(t.Primary).Bold(true),
		Hint:           lipgloss.NewStyle().Foreground(t.Muted),
		Input:          lipgloss.NewStyle().Foreground(bodyFG),
		InputFocused:   lipgloss.NewStyle().Foreground(lipgloss.Color("#ffffff")).Bold(true),
		OverlayBox: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(t.Primary).
			Background(t.Surface).
			Padding(1, 2),
		OverlayTitle: lipgloss.NewStyle().Foreground(t.Primary).Bold(true),
		Decision:     lipgloss.NewStyle().Foreground(t.Success),
		Action:       lipgloss.NewStyle().Foreground(t.Info),
		Risk:         lipgloss.NewStyle().Foreground(t.Warning),
		Question:     lipgloss.NewStyle().Foreground(t.Secondary),
		SpeakerA:     lipgloss.NewStyle().Foreground(t.SpeakerA).Bold(true),
		SpeakerB:     lipgloss.NewStyle().Foreground(t.SpeakerB).Bold(true),
		SpeakerC:     lipgloss.NewStyle().Foreground(t.SpeakerC).Bold(true),
		Citation:     lipgloss.NewStyle().Foreground(t.Secondary),
		MeterFilled:  lipgloss.NewStyle().Foreground(t.Success),
		MeterEmpty:   lipgloss.NewStyle().Foreground(t.Muted),
		MeterClip:    lipgloss.NewStyle().Foreground(t.Danger).Bold(true),
		// FTS hit highlight: yellow background, dark fg. The "active"
		// variant marks the segment `n`/`N` is currently parked on so
		// the user can tell where they jumped to.
		Highlight:       lipgloss.NewStyle().Foreground(lipgloss.Color("#0b0f17")).Background(t.Warning).Bold(true),
		HighlightActive: lipgloss.NewStyle().Foreground(lipgloss.Color("#0b0f17")).Background(t.Primary).Bold(true),
	}
}
