package tui

import (
	"image/color"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/lukasstrickler/noto/internal/transport/notoapi"
	"github.com/lukasstrickler/noto/internal/ui/tui/layout"
	"github.com/lukasstrickler/noto/internal/ui/tui/theme"
)

// speakerStat aggregates per-speaker talk time, turn count, word count
// and a small set of salient 3-word phrases. Used by the detail pane's
// Speakers tab.
type speakerStat struct {
	ID   string
	Name string
	// token is the stable anonymous per-meeting label ("Speaker A") — the handle
	// the LLM was given. The renderer resolves it back to a person + color.
	token      string
	TalkSec    float64
	TurnCount  int
	WordCount  int
	TopPhrases []string
	colorIdx   int
}

func computeSpeakerStats(t notoapi.Transcript) []speakerStat {
	if len(t.Segments) == 0 {
		return nil
	}
	type acc struct {
		talk  float64
		turns int
		words int
		text  strings.Builder
		name  string
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
	// The anonymous per-meeting token ("Speaker A") lives on the Speaker, not the
	// segments — index it so each stat can resolve back to a person + color.
	tokenByID := make(map[string]string, len(t.Speakers))
	for _, sp := range t.Speakers {
		tokenByID[sp.ID] = sp.Label
	}
	out := make([]speakerStat, 0, len(bag))
	for id, a := range bag {
		name := a.name
		if name == "" {
			name = id
		}
		token := tokenByID[id]
		if token == "" {
			token = name
		}
		out = append(out, speakerStat{
			ID:         id,
			Name:       name,
			token:      token,
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

func approxWordCount(s string) int {
	return len(strings.Fields(s))
}

// topPhrases returns up to n distinctive 3-word phrases by frequency,
// filtering stopwords. Cheap heuristic; not a real keyphrase extractor.
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

// shareBar renders a horizontal share bar (0..1) in the speaker's color.
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
	c := speakerColorForIndex(st.T, colorIdx)
	fillStyle := lipgloss.NewStyle().Foreground(c)
	return fillStyle.Render(strings.Repeat("█", filled)) + st.Muted.Render(strings.Repeat("·", width-filled))
}

// speakerExtra extends SpeakerA/B/C (purple/pink/teal) with three more
// identity hues for meetings with 4–6 speakers. Picked to stay clear of
// the semantic palette: no blue (Primary), cyan (Info), green (Success),
// amber (Warning) or red (Danger), so a colored name never reads as a
// status. The old sky/sand extras were dropped — they collided with the
// new blue brand and the amber warning respectively.
var speakerExtra = []color.Color{
	lipgloss.Color("#fb923c"), // orange-400
	lipgloss.Color("#a78bfa"), // violet-400
	lipgloss.Color("#fda4af"), // rose-300
}

func speakerColorForIndex(t theme.Theme, idx int) color.Color {
	switch idx % 6 {
	case 0:
		return t.SpeakerA
	case 1:
		return t.SpeakerB
	case 2:
		return t.SpeakerC
	default:
		return speakerExtra[(idx-3)%len(speakerExtra)]
	}
}

// panelEmpty renders a single-panel empty state. Used by screens that
// need a "loading…" or error placeholder without a body.
func panelEmpty(ctx screenCtx, title, body string) string {
	return layout.Panel{Title: title, Width: ctx.width, Height: ctx.height, Focused: true, Body: body}.Render(ctx.styles)
}
