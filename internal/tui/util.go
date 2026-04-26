package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/lukasstrickler/noto/internal/providers"
)

func providerByID(suites []providers.ProviderSuite, id string) (providers.ProviderSuite, bool) {
	for _, suite := range suites {
		if suite.ID == id {
			return suite, true
		}
	}
	return providers.ProviderSuite{ID: id, DisplayName: id}, false
}

func providerEnv(ref string) string {
	switch ref {
	case "provider:mistral":
		return "MISTRAL_API_KEY"
	case "provider:assemblyai":
		return "ASSEMBLYAI_API_KEY"
	case "provider:elevenlabs":
		return "ELEVENLABS_API_KEY"
	case "provider:openrouter":
		return "OPENROUTER_API_KEY"
	default:
		return ""
	}
}

func firstModel(suite providers.ProviderSuite) string {
	if len(suite.Models) == 0 {
		return ""
	}
	return suite.Models[0].ID
}

func boolText(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

func normalizedSize(width int, height int) (int, int) {
	if width < 72 {
		width = 72
	}
	if height < 24 {
		height = 24
	}
	return width, height
}

func (m AppModel) width() int {
	width, _ := normalizedSize(m.UI.Width, m.UI.Height)
	return width
}

func (m AppModel) height() int {
	_, height := normalizedSize(m.UI.Width, m.UI.Height)
	return height
}

func (m AppModel) contentWidth() int {
	width := m.width()
	if m.compactLayout() {
		return width
	}
	return width - m.sidebarWidth() - 1
}

func (m AppModel) contentHeight() int {
	height := m.height()
	h := height - 3
	if m.UI.Banner != nil {
		h--
	}
	return max(12, h)
}

func (m AppModel) compactLayout() bool {
	return m.width() < 96
}

func (m AppModel) sidebarWidth() int {
	return clamp(m.width()/5, 24, 34)
}

func (m AppModel) missingKeyCount() int {
	count := 0
	for _, id := range []string{"mistral", "assemblyai", "elevenlabs", "openrouter"} {
		if !m.App.Statuses[id].Configured {
			count++
		}
	}
	return count
}

func (m AppModel) speechConfigured() bool {
	for _, id := range []string{"mistral", "assemblyai", "elevenlabs"} {
		if m.App.Statuses[id].Configured {
			return true
		}
	}
	return false
}

func (m AppModel) recorderBadge() string {
	if !m.speechConfigured() {
		return "blocked"
	}
	return m.App.Recorder.State
}

func (m AppModel) jobSummary() string {
	for _, job := range m.App.Jobs {
		if job.Status != "idle" && job.Status != "clean" {
			return job.Name + " " + job.Status
		}
	}
	return "idle"
}

func (m *AppModel) syncSearch(query string) {
	store := m.Runtime.Meetings
	if store == nil {
		fixtures := fixtureStore{meetings: m.App.Meetings}
		store = fixtures
	}
	m.App.SearchResults = store.SearchSegments(query)
	if len(m.App.SearchResults) == 0 {
		m.UI.SelectedResult = 0
		return
	}
	m.UI.SelectedResult = clamp(m.UI.SelectedResult, 0, len(m.App.SearchResults)-1)
}

func (m AppModel) filteredSearchResults() []SearchResult {
	store := m.Runtime.Meetings
	if store == nil {
		store = fixtureStore{meetings: m.App.Meetings}
	}
	return store.SearchSegments(m.UI.SearchQuery)
}

type commandRow struct {
	label  string
	hint   string
	action ActionID
}

func (m AppModel) commandRows() []commandRow {
	all := []commandRow{
		{"record", "open recorder preflight", ActionRecord},
		{"import", "import transcript/audio (pending)", ActionImport},
		{"providers", "configure STT and OpenRouter", ActionProviders},
		{"verify", "run local verification", ActionVerify},
		{"settings", "edit local paths and routing", ActionEdit},
		{"search", "search local evidence", ActionSearch},
	}
	q := strings.ToLower(strings.TrimSpace(m.UI.Overlay.Query))
	if q == "" {
		return all
	}
	var rows []commandRow
	for _, row := range all {
		if strings.Contains(row.label+" "+row.hint, q) {
			rows = append(rows, row)
		}
	}
	return rows
}

func citationFor(result SearchResult) string {
	if result.MeetingID == "" {
		return ""
	}
	return fmt.Sprintf("noto transcript --json %s#%s", result.MeetingID, result.Segment.ID)
}

func splitWidths(width int) (int, int) {
	if width < 92 {
		left := clamp(width/2, 28, 40)
		return left, width - left - 1
	}
	left := width / 2
	return left, width - left - 1
}

func threeColumnWidths(width int) (int, int, int) {
	left := clamp(width/4, 24, 34)
	mid := clamp(width/3, 30, 54)
	right := width - left - mid - 2
	if right < 24 {
		left, right = splitWidths(width)
		return left, right, 0
	}
	return left, mid, right
}

func sectionTitle(label string) string {
	return labelStyle.Render(strings.ToUpper(label))
}

func detailBlock(width int, heading string, lines []string) string {
	var b strings.Builder
	b.WriteString(labelStyle.Render(strings.ToUpper(heading)))
	b.WriteString("\n")
	b.WriteString(detailLines(width, lines))
	return b.String()
}

func detailLines(width int, lines []string) string {
	var b strings.Builder
	for _, line := range lines {
		if line == "" {
			b.WriteString("\n")
			continue
		}
		b.WriteString("  ")
		b.WriteString(mutedStyle.Render(fit(line, max(8, width-8))))
		b.WriteString("\n")
	}
	return b.String()
}

func fit(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= width {
		return s
	}
	if width <= 1 {
		return "..."
	}
	runes := []rune(s)
	if len(runes) > width-1 {
		runes = runes[:width-1]
	}
	return string(runes) + "..."
}

func padRight(s string, width int) string {
	if lipgloss.Width(s) >= width {
		return fit(s, width)
	}
	return s + strings.Repeat(" ", width-lipgloss.Width(s))
}

func activeLabel(screen Screen) string {
	if screen == "" {
		return "Dashboard"
	}
	return strings.ToUpper(string(screen[:1])) + string(screen[1:])
}

func clamp(n int, minValue int, maxValue int) int {
	if maxValue < minValue {
		return minValue
	}
	if n < minValue {
		return minValue
	}
	if n > maxValue {
		return maxValue
	}
	return n
}

func wrapIndex(current int, delta int, length int) int {
	if length <= 0 {
		return 0
	}
	next := (current + delta) % length
	if next < 0 {
		next += length
	}
	return next
}

func max(a int, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a int, b int) int {
	if a < b {
		return a
	}
	return b
}
