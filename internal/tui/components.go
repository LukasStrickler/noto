package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

type Panel struct {
	Title    string
	Subtitle string
	Width    int
	Height   int
	Focused  bool
	Body     string
}

func (p Panel) Render() string {
	header := sectionTitle(p.Title)
	if p.Subtitle != "" {
		header += " " + styles.Muted.Render(p.Subtitle)
	}
	body := p.Body
	if p.Title != "" {
		body = header + "\n" + body
	}
	return panelStyleFor(p.Width, p.Height, p.Focused).Render(body)
}

type Frame = Panel

type TableRow struct {
	Selected bool
	Cells    []string
}

func renderTable(width int, rows []TableRow, colWidths ...int) string {
	var b strings.Builder
	for _, row := range rows {
		style := styles.Row
		prefix := " "
		if row.Selected {
			style = styles.RowSelected
			prefix = ">"
		}
		parts := make([]string, 0, len(row.Cells)+1)
		parts = append(parts, prefix)
		for i, cell := range row.Cells {
			cellWidth := 0
			if i < len(colWidths) {
				cellWidth = colWidths[i]
			}
			if cellWidth <= 0 {
				parts = append(parts, cell)
				continue
			}
			parts = append(parts, fit(cell, cellWidth))
		}
		b.WriteString(style.Width(max(8, width-4)).Render(fit(strings.Join(parts, " "), max(4, width-8))))
		b.WriteString("\n")
	}
	if len(rows) == 0 {
		b.WriteString(styles.Muted.Render("  no rows"))
	}
	return b.String()
}

func renderMeter(label string, db int, width int) string {
	inner := clamp(width-lipgloss.Width(label)-14, 8, 30)
	level := clamp((db+60)*inner/60, 0, inner)
	bar := strings.Repeat("#", level) + strings.Repeat(".", inner-level)
	return fit(label+" ["+bar+"] "+formatDB(db), width)
}

func renderInput(label string, value string, masked bool, width int) string {
	display := value
	if masked && value != "" {
		display = strings.Repeat("*", len([]rune(value)))
	}
	if display == "" {
		display = "<empty>"
	}
	return styles.Title.Render(label) + "\n" + styles.Input.Width(max(12, width-4)).Render(fit(display, max(8, width-8)))
}

func overlay(base string, modal string, width int, height int) string {
	if modal == "" {
		return lipgloss.NewStyle().Width(width).Height(height).Render(base)
	}
	return lipgloss.NewStyle().Width(width).Height(height).Render(base) + modalOverlayCommands(modal, width, height)
}

func modalOverlayCommands(modal string, width int, height int) string {
	modalWidth := clamp(lipgloss.Width(modal)+4, 52, min(width-8, 92))
	modalHeight := clamp(lipgloss.Height(modal)+2, 8, min(height-4, 22))
	box := styles.Modal.Width(max(10, modalWidth-4)).Height(max(4, modalHeight-2)).Render(modal)
	x := max(0, (width-lipgloss.Width(box))/2)
	y := max(0, (height-lipgloss.Height(box))/2)
	lines := strings.Split(box, "\n")
	var b strings.Builder
	b.WriteString("\x1b[s")
	for i, line := range lines {
		b.WriteString(fmt.Sprintf("\x1b[%d;%dH%s", y+i+1, x+1, line))
	}
	b.WriteString("\x1b[u")
	return b.String()
}

func formatDB(db int) string {
	if db >= 0 {
		return "CLIP"
	}
	return fmt.Sprintf("%ddB", db)
}
