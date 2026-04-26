package tui

import "github.com/charmbracelet/lipgloss"

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
