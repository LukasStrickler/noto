package tui

import "github.com/charmbracelet/lipgloss"

type SemanticColors struct {
	Decision lipgloss.Color
	Action   lipgloss.Color
	Risk     lipgloss.Color
	SpeakerA lipgloss.Color
	SpeakerB lipgloss.Color
	SpeakerC lipgloss.Color
	Info     lipgloss.Color
	Success  lipgloss.Color
}

type Theme struct {
	BG       lipgloss.Color
	Surface  lipgloss.Color
	FG       lipgloss.Color
	Muted    lipgloss.Color
	Border   lipgloss.Color
	Focused  lipgloss.Color
	Selected lipgloss.Color
	Accent   lipgloss.Color
	Key      lipgloss.Color
	Warning  lipgloss.Color
	Error    lipgloss.Color
	Success  lipgloss.Color
	Danger   lipgloss.Color
	Disabled lipgloss.Color

	Semantic SemanticColors
}

var darkTheme = Theme{
	BG:       lipgloss.Color("#0d1117"),
	Surface:  lipgloss.Color("#111827"),
	FG:       lipgloss.Color("#e5e7eb"),
	Muted:    lipgloss.Color("#8b949e"),
	Border:   lipgloss.Color("#30363d"),
	Focused:  lipgloss.Color("#58a6ff"),
	Selected: lipgloss.Color("#1f2937"),
	Accent:   lipgloss.Color("#7dd3fc"),
	Key:      lipgloss.Color("#a7f3d0"),
	Warning:  lipgloss.Color("#fbbf24"),
	Error:    lipgloss.Color("#f87171"),
	Success:  lipgloss.Color("#34d399"),
	Danger:   lipgloss.Color("#fb7185"),
	Disabled: lipgloss.Color("#64748b"),
	Semantic: SemanticColors{
		Decision: lipgloss.Color("#34d399"),
		Action:   lipgloss.Color("#fbbf24"),
		Risk:     lipgloss.Color("#f87171"),
		SpeakerA: lipgloss.Color("#22d3ee"),
		SpeakerB: lipgloss.Color("#a78bfa"),
		SpeakerC: lipgloss.Color("#fb923c"),
		Info:     lipgloss.Color("#7dd3fc"),
		Success:  lipgloss.Color("#34d399"),
	},
}

var lightTheme = Theme{
	BG:       lipgloss.Color("#f8fafc"),
	Surface:  lipgloss.Color("#f1f5f9"),
	FG:       lipgloss.Color("#1e293b"),
	Muted:    lipgloss.Color("#64748b"),
	Border:   lipgloss.Color("#cbd5e1"),
	Focused:  lipgloss.Color("#3b82f6"),
	Selected: lipgloss.Color("#e2e8f0"),
	Accent:   lipgloss.Color("#0284c7"),
	Key:      lipgloss.Color("#166534"),
	Warning:  lipgloss.Color("#854d0e"),
	Error:    lipgloss.Color("#dc2626"),
	Success:  lipgloss.Color("#16a34a"),
	Danger:   lipgloss.Color("#e11d48"),
	Disabled: lipgloss.Color("#94a3b8"),
	Semantic: SemanticColors{
		Decision: lipgloss.Color("#16a34a"),
		Action:   lipgloss.Color("#854d0e"),
		Risk:     lipgloss.Color("#dc2626"),
		SpeakerA: lipgloss.Color("#0891b2"),
		SpeakerB: lipgloss.Color("#7c3aed"),
		SpeakerC: lipgloss.Color("#ea580c"),
		Info:     lipgloss.Color("#0284c7"),
		Success:  lipgloss.Color("#16a34a"),
	},
}

var currentTheme = darkTheme

type ThemeManager struct {
	isDark bool
}

func NewThemeManager() *ThemeManager {
	return &ThemeManager{isDark: true}
}

func (tm *ThemeManager) SetDark(dark bool) {
	tm.isDark = dark
	if dark {
		currentTheme = darkTheme
	} else {
		currentTheme = lightTheme
	}
}

func (tm *ThemeManager) IsDark() bool {
	return tm.isDark
}

func (tm *ThemeManager) Current() Theme {
	if tm.isDark {
		return darkTheme
	}
	return lightTheme
}

type SemanticStyles struct {
	Decision lipgloss.Style
	Action   lipgloss.Style
	Risk     lipgloss.Style
	SpeakerA lipgloss.Style
	SpeakerB lipgloss.Style
	SpeakerC lipgloss.Style
	Info     lipgloss.Style
	Success  lipgloss.Style
}

func NewSemanticStyles(t Theme) SemanticStyles {
	return SemanticStyles{
		Decision: lipgloss.NewStyle().Foreground(t.Semantic.Decision).Bold(true),
		Action:   lipgloss.NewStyle().Foreground(t.Semantic.Action).Bold(true),
		Risk:     lipgloss.NewStyle().Foreground(t.Semantic.Risk).Bold(false),
		SpeakerA: lipgloss.NewStyle().Foreground(t.Semantic.SpeakerA),
		SpeakerB: lipgloss.NewStyle().Foreground(t.Semantic.SpeakerB),
		SpeakerC: lipgloss.NewStyle().Foreground(t.Semantic.SpeakerC),
		Info:     lipgloss.NewStyle().Foreground(t.Semantic.Info),
		Success:  lipgloss.NewStyle().Foreground(t.Semantic.Success),
	}
}

type StyleSet struct {
	Panel        lipgloss.Style
	PanelFocused lipgloss.Style
	Header       lipgloss.Style
	HeaderStrong lipgloss.Style
	HeaderMeta   lipgloss.Style
	Title        lipgloss.Style
	Label        lipgloss.Style
	Muted        lipgloss.Style
	Rule         lipgloss.Style
	Row          lipgloss.Style
	RowSelected  lipgloss.Style
	Chip         lipgloss.Style
	ChipKey      lipgloss.Style
	ChipLabel    lipgloss.Style
	ChipDisabled lipgloss.Style
	Input        lipgloss.Style
	Modal        lipgloss.Style
	BannerInfo   lipgloss.Style
	BannerWarn   lipgloss.Style
	BannerError  lipgloss.Style
	Success      lipgloss.Style
	Warning      lipgloss.Style
	Danger       lipgloss.Style
}

var defaultTheme = Theme{
	BG:       lipgloss.Color("#0d1117"),
	Surface:  lipgloss.Color("#111827"),
	FG:       lipgloss.Color("#e5e7eb"),
	Muted:    lipgloss.Color("#8b949e"),
	Border:   lipgloss.Color("#30363d"),
	Focused:  lipgloss.Color("#58a6ff"),
	Selected: lipgloss.Color("#1f2937"),
	Accent:   lipgloss.Color("#7dd3fc"),
	Key:      lipgloss.Color("#a7f3d0"),
	Warning:  lipgloss.Color("#fbbf24"),
	Error:    lipgloss.Color("#f87171"),
	Success:  lipgloss.Color("#34d399"),
	Danger:   lipgloss.Color("#fb7185"),
	Disabled: lipgloss.Color("#64748b"),
}

var styles = NewStyleSet(defaultTheme)

var (
	topTitleStyle          = styles.HeaderStrong
	topMetaStyle           = styles.HeaderMeta
	titleStyle             = styles.Title
	labelStyle             = styles.Label
	mutedStyle             = styles.Muted
	ruleStyle              = styles.Rule
	navStyle               = styles.Row
	navActiveStyle         = styles.RowSelected
	listStyle              = styles.Row
	listActiveStyle        = styles.RowSelected
	footerStyle            = styles.Header
	actionChipStyle        = styles.Chip
	actionKeyStyle         = styles.ChipKey
	actionLabelStyle       = styles.ChipLabel
	disabledChipFrameStyle = styles.ChipDisabled
	disabledChipStyle      = styles.ChipDisabled
	inputStyle             = styles.Input
	bannerInfoStyle        = styles.BannerInfo
	bannerWarnStyle        = styles.BannerWarn
	bannerErrorStyle       = styles.BannerError
)

func NewStyleSet(theme Theme) StyleSet {
	border := lipgloss.RoundedBorder()
	return StyleSet{
		Panel: lipgloss.NewStyle().
			Border(border).
			BorderForeground(theme.Border).
			Background(theme.BG).
			Foreground(theme.FG).
			Padding(0, 1),
		PanelFocused: lipgloss.NewStyle().
			Border(border).
			BorderForeground(theme.Focused).
			Background(theme.BG).
			Foreground(theme.FG).
			Padding(0, 1),
		Header: lipgloss.NewStyle().
			Foreground(theme.FG).
			Background(theme.Surface),
		HeaderStrong: lipgloss.NewStyle().
			Foreground(theme.BG).
			Background(theme.Accent).
			Bold(true).
			Padding(0, 1),
		HeaderMeta: lipgloss.NewStyle().
			Foreground(theme.Muted).
			Background(theme.Surface).
			Padding(0, 1),
		Title: lipgloss.NewStyle().
			Foreground(theme.FG).
			Bold(true),
		Label: lipgloss.NewStyle().
			Foreground(theme.Accent).
			Bold(true),
		Muted: lipgloss.NewStyle().
			Foreground(theme.Muted),
		Rule: lipgloss.NewStyle().
			Foreground(theme.Border),
		Row: lipgloss.NewStyle().
			Foreground(theme.FG).
			Padding(0, 1),
		RowSelected: lipgloss.NewStyle().
			Foreground(theme.FG).
			Background(theme.Selected).
			Bold(true).
			Padding(0, 1),
		Chip: lipgloss.NewStyle().
			Foreground(theme.FG).
			Background(theme.Surface),
		ChipKey: lipgloss.NewStyle().
			Foreground(theme.BG).
			Background(theme.Key).
			Bold(true),
		ChipLabel: lipgloss.NewStyle().
			Foreground(theme.FG),
		ChipDisabled: lipgloss.NewStyle().
			Foreground(theme.Disabled).
			Background(theme.Surface),
		Input: lipgloss.NewStyle().
			Foreground(theme.FG).
			Background(lipgloss.Color("#0b1220")).
			Padding(0, 1),
		Modal: lipgloss.NewStyle().
			Border(border).
			BorderForeground(theme.Focused).
			Background(theme.Surface).
			Foreground(theme.FG).
			Padding(0, 1),
		BannerInfo: lipgloss.NewStyle().
			Foreground(theme.BG).
			Background(theme.Accent),
		BannerWarn: lipgloss.NewStyle().
			Foreground(theme.BG).
			Background(theme.Warning),
		BannerError: lipgloss.NewStyle().
			Foreground(theme.BG).
			Background(theme.Error).
			Bold(true),
		Success: lipgloss.NewStyle().
			Foreground(theme.Success).
			Bold(true),
		Warning: lipgloss.NewStyle().
			Foreground(theme.Warning).
			Bold(true),
		Danger: lipgloss.NewStyle().
			Foreground(theme.Danger).
			Bold(true),
	}
}

func panelStyle(width int, height int) lipgloss.Style {
	return styles.Panel.Width(max(10, width-4)).Height(max(4, height-2))
}

func panelStyleFor(width int, height int, focused bool) lipgloss.Style {
	if focused {
		return styles.PanelFocused.Width(max(10, width-4)).Height(max(4, height-2))
	}
	return panelStyle(width, height)
}
