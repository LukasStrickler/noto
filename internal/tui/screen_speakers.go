package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/lipgloss"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/lukasstrickler/noto/internal/notoapi"
	"github.com/lukasstrickler/noto/internal/tui/keys"
	"github.com/lukasstrickler/noto/internal/tui/layout"
	"github.com/lukasstrickler/noto/internal/tui/theme"
)

// speakersScreen visualizes who-talked-when: per-speaker talk-time
// stats, a colored timeline strip, and a per-speaker word cloud. It is
// built on top of the same transcript the detail/transcript screens
// consume; if a meeting has no diarization yet it shows a hint to run
// AssemblyAI with `speaker_labels=true` (the default in our adapter).
type speakersScreen struct {
	id_     string
	tr      notoapi.Transcript
	stats   []speakerStat
	cursor  int
	loading bool
	err     error
}

type speakerStat struct {
	ID         string
	Name       string
	TalkSec    float64
	TurnCount  int
	WordCount  int
	TopPhrases []string
	colorIdx   int
}

func newSpeakersScreen() screen { return &speakersScreen{loading: true} }

func (s *speakersScreen) id() screenID         { return sSpeakers }
func (s *speakersScreen) title() string        { return "speakers" }
func (s *speakersScreen) helpKeys() []keys.Map { return nil }
func (s *speakersScreen) inputActive() bool    { return false }
func (s *speakersScreen) meetingID() string    { return s.id_ }

func (s *speakersScreen) enter(ctx screenCtx, param string) tea.Cmd {
	s.id_ = param
	s.loading = true
	return fetchTranscript(ctx, param)
}
func (s *speakersScreen) leave(_ screenCtx) tea.Cmd { return nil }

func (s *speakersScreen) update(ctx screenCtx, msg tea.Msg) (screen, tea.Cmd) {
	switch v := msg.(type) {
	case transcriptLoadedMsg:
		s.loading = false
		s.err = v.Err
		s.tr = v.Transcript
		s.stats = computeSpeakerStats(s.tr)
		if s.cursor >= len(s.stats) {
			s.cursor = 0
		}
	case tea.KeyMsg:
		switch {
		case key.Matches(v, ctx.keys.Up):
			if s.cursor > 0 {
				s.cursor--
			}
		case key.Matches(v, ctx.keys.Down):
			if s.cursor < len(s.stats)-1 {
				s.cursor++
			}
		case v.String() == "t":
			return s, push(sTranscript, s.id_)
		}
	}
	return s, nil
}

func (s *speakersScreen) view(ctx screenCtx) string {
	st := ctx.styles
	if s.loading {
		return panelEmpty(ctx, "speakers", "loading transcript…")
	}
	if s.err != nil {
		return panelEmpty(ctx, "speakers", st.BadgeDanger.Render(s.err.Error()))
	}
	if len(s.stats) == 0 {
		hint := strings.Join([]string{
			st.Muted.Render("No diarization in this transcript yet."),
			"",
			st.Muted.Render("Speaker labels appear after AssemblyAI runs with"),
			st.Muted.Render("`speaker_labels=true` (on by default in noto)."),
			"",
			st.Muted.Render("Add an AssemblyAI key in providers (4) and re-run"),
			st.Muted.Render("the pipeline, or import audio with `noto import-audio`."),
		}, "\n")
		return panelEmpty(ctx, "speakers — "+s.id_, hint)
	}

	bp := layout.BreakpointFor(ctx.width)
	if bp == layout.Narrow {
		body := s.renderHeader(ctx) + "\n\n" + s.renderList(ctx, ctx.width-6) + "\n\n" + s.renderTimeline(ctx, ctx.width-6)
		return layout.Panel{Title: "speakers", Subtitle: fmt.Sprintf("%d voices · %s talk", len(s.stats), formatDuration(int(totalTalk(s.stats)))), Width: ctx.width, Height: ctx.height, Focused: true, Body: body}.Render(st)
	}
	leftW := ctx.width * 5 / 12
	rightW := ctx.width - leftW - 1
	leftBody := s.renderHeader(ctx) + "\n\n" + s.renderList(ctx, leftW-4)
	rightBody := s.renderDetail(ctx, rightW-4) + "\n\n" + s.renderTimeline(ctx, rightW-4)
	left := layout.Panel{Title: "voices", Subtitle: fmt.Sprintf("%d", len(s.stats)), Width: leftW, Height: ctx.height, Focused: true, Body: leftBody}.Render(st)
	right := layout.Panel{Title: "detail", Width: rightW, Height: ctx.height, Body: rightBody}.Render(st)
	return lipgloss.JoinHorizontal(lipgloss.Top, left, " ", right)
}

func (s *speakersScreen) renderHeader(ctx screenCtx) string {
	st := ctx.styles
	total := totalTalk(s.stats)
	dur := s.tr.Segments
	span := 0.0
	if len(dur) > 0 {
		span = dur[len(dur)-1].EndSec
	}
	return strings.Join([]string{
		st.HeaderEm.Render("Who talked, and how much"),
		st.Muted.Render(fmt.Sprintf("%s of speech across %s · %d turns", formatDuration(int(total)), formatDuration(int(span)), totalTurns(s.stats))),
	}, "\n")
}

func (s *speakersScreen) renderList(ctx screenCtx, width int) string {
	st := ctx.styles
	total := totalTalk(s.stats)
	if total <= 0 {
		total = 1
	}
	rows := []string{}
	for i, sp := range s.stats {
		share := sp.TalkSec / total
		bar := shareBar(st, share, max(8, width-32), sp.colorIdx)
		name := fit(sp.Name, max(8, width-32))
		stat := fmt.Sprintf("%5s  %4.0f%%", formatDuration(int(sp.TalkSec)), share*100)
		line := fmt.Sprintf("%s  %s  %s", name, bar, st.Muted.Render(stat))
		if i == s.cursor {
			line = st.RowSelected.Render("› " + line)
		} else {
			line = "  " + line
		}
		rows = append(rows, line)
	}
	return strings.Join(rows, "\n")
}

func (s *speakersScreen) renderDetail(ctx screenCtx, width int) string {
	st := ctx.styles
	if s.cursor >= len(s.stats) {
		return st.Muted.Render("Select a speaker.")
	}
	sp := s.stats[s.cursor]
	bullets := []string{}
	for _, ph := range sp.TopPhrases {
		if len(ph) > width-4 {
			ph = ph[:width-5] + "…"
		}
		bullets = append(bullets, "  • "+ph)
	}
	if len(bullets) == 0 {
		bullets = append(bullets, st.Muted.Render("  (no salient phrases yet)"))
	}
	speakerColor := speakerColorForIndex(st.T, sp.colorIdx)
	chip := lipgloss.NewStyle().Foreground(speakerColor).Bold(true).Render("●  " + sp.Name)
	return strings.Join([]string{
		chip,
		st.Muted.Render(fmt.Sprintf("%s · %d turns · ~%d words", formatDuration(int(sp.TalkSec)), sp.TurnCount, sp.WordCount)),
		"",
		st.PanelTitle.Render("salient phrases"),
		strings.Join(bullets, "\n"),
	}, "\n")
}

// renderTimeline draws a single-row colored strip showing who spoke
// when across the meeting timeline. Width is in cells; each cell maps
// to a time bucket and is colored by the dominant speaker in that
// bucket.
func (s *speakersScreen) renderTimeline(ctx screenCtx, width int) string {
	st := ctx.styles
	if width < 10 || len(s.tr.Segments) == 0 {
		return ""
	}
	end := s.tr.Segments[len(s.tr.Segments)-1].EndSec
	if end <= 0 {
		return ""
	}
	bucketSec := end / float64(width)
	if bucketSec <= 0 {
		return ""
	}

	colorIdx := map[string]int{}
	for _, sp := range s.stats {
		colorIdx[sp.ID] = sp.colorIdx
	}

	// For each bucket: count seconds per speaker, take max.
	buckets := make([][]float64, width)
	for i := range buckets {
		buckets[i] = make([]float64, len(s.stats))
	}
	speakerIdx := map[string]int{}
	for i, sp := range s.stats {
		speakerIdx[sp.ID] = i
	}
	for _, seg := range s.tr.Segments {
		idx, ok := speakerIdx[seg.SpeakerID]
		if !ok {
			continue
		}
		start := int(seg.StartSec / bucketSec)
		stop := int(seg.EndSec / bucketSec)
		if stop >= width {
			stop = width - 1
		}
		if start < 0 {
			start = 0
		}
		for b := start; b <= stop && b < width; b++ {
			buckets[b][idx] += seg.EndSec - seg.StartSec
		}
	}

	var b strings.Builder
	for _, bk := range buckets {
		bestIdx, best := -1, 0.0
		for i, v := range bk {
			if v > best {
				best = v
				bestIdx = i
			}
		}
		if bestIdx < 0 {
			b.WriteString(st.Muted.Render("·"))
			continue
		}
		c := speakerColorForIndex(st.T, s.stats[bestIdx].colorIdx)
		b.WriteString(lipgloss.NewStyle().Foreground(c).Render("█"))
	}
	axis := st.Muted.Render("0:00") + strings.Repeat(" ", max(0, width-10)) + st.Muted.Render(formatDuration(int(end)))
	return strings.Join([]string{
		st.PanelTitle.Render("timeline"),
		b.String(),
		axis,
	}, "\n")
}

// --- aggregation ---

func computeSpeakerStats(t notoapi.Transcript) []speakerStat {
	if len(t.Segments) == 0 {
		return nil
	}
	type acc struct {
		talk   float64
		turns  int
		words  int
		text   strings.Builder
		name   string
	}
	bag := map[string]*acc{}
	for _, seg := range t.Segments {
		a, ok := bag[seg.SpeakerID]
		if !ok {
			a = &acc{name: seg.Speaker}
			bag[seg.SpeakerID] = a
		}
		a.talk += seg.EndSec - seg.StartSec
		a.turns++
		a.words += approxWordCount(seg.Text)
		if a.name == "" {
			a.name = seg.Speaker
		}
		a.text.WriteString(seg.Text)
		a.text.WriteString(" ")
	}
	// Stable order: descending by talk time.
	out := make([]speakerStat, 0, len(bag))
	for id, a := range bag {
		name := a.name
		if name == "" {
			name = id
		}
		out = append(out, speakerStat{
			ID:         id,
			Name:       name,
			TalkSec:    a.talk,
			TurnCount:  a.turns,
			WordCount:  a.words,
			TopPhrases: topPhrases(a.text.String(), 5),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TalkSec > out[j].TalkSec })
	for i := range out {
		out[i].colorIdx = i % 6
	}
	return out
}

func totalTalk(stats []speakerStat) float64 {
	t := 0.0
	for _, s := range stats {
		t += s.TalkSec
	}
	return t
}

func totalTurns(stats []speakerStat) int {
	t := 0
	for _, s := range stats {
		t += s.TurnCount
	}
	return t
}

func approxWordCount(s string) int {
	return len(strings.Fields(s))
}

// topPhrases returns up to n distinctive phrases (3-5 word windows) by
// frequency, with stopwords filtered. This is the cheap "what did this
// speaker keep saying" hint; not a real keyphrase extractor.
func topPhrases(s string, n int) []string {
	words := strings.Fields(strings.ToLower(s))
	if len(words) < 3 {
		return nil
	}
	counts := map[string]int{}
	for i := 0; i < len(words)-2; i++ {
		w0, w1, w2 := words[i], words[i+1], words[i+2]
		if stopword(w0) || stopword(w2) {
			continue
		}
		phrase := strings.TrimFunc(w0+" "+w1+" "+w2, isPunct)
		if len(phrase) < 8 {
			continue
		}
		counts[phrase]++
	}
	type kv struct {
		k string
		v int
	}
	all := make([]kv, 0, len(counts))
	for k, v := range counts {
		if v >= 2 {
			all = append(all, kv{k, v})
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].v > all[j].v })
	if len(all) > n {
		all = all[:n]
	}
	out := make([]string, 0, len(all))
	for _, p := range all {
		out = append(out, p.k)
	}
	return out
}

func stopword(w string) bool {
	switch w {
	case "the", "a", "an", "and", "or", "but", "so", "to", "of", "for", "on", "at",
		"in", "is", "are", "was", "were", "be", "been", "being", "this", "that",
		"i", "you", "we", "they", "he", "she", "it", "me", "us", "them",
		"do", "does", "did", "have", "has", "had", "will", "would", "could", "should":
		return true
	}
	return false
}

func isPunct(r rune) bool {
	switch r {
	case '.', ',', '!', '?', ';', ':', '"', '\'', '(', ')':
		return true
	}
	return false
}

// shareBar renders a horizontal bar showing share (0..1) in the
// speaker's color.
func shareBar(st theme.Styles, share float64, width, colorIdx int) string {
	if width < 4 {
		width = 4
	}
	filled := int(share * float64(width))
	if filled > width {
		filled = width
	}
	if filled < 1 && share > 0 {
		filled = 1
	}
	color := speakerColorForIndex(st.T, colorIdx)
	fillStyle := lipgloss.NewStyle().Foreground(color)
	return fillStyle.Render(strings.Repeat("█", filled)) + st.Muted.Render(strings.Repeat("·", width-filled))
}

func speakerColorForIndex(t theme.Theme, idx int) lipgloss.Color {
	switch idx % 6 {
	case 0:
		return t.SpeakerA
	case 1:
		return t.SpeakerB
	case 2:
		return t.SpeakerC
	case 3:
		return t.Info
	case 4:
		return t.Success
	default:
		return t.Warning
	}
}

func panelEmpty(ctx screenCtx, title, body string) string {
	return layout.Panel{Title: title, Width: ctx.width, Height: ctx.height, Focused: true, Body: body}.Render(ctx.styles)
}
