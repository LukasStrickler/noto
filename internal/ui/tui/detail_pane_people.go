package tui

import (
	"fmt"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/lukasstrickler/noto/internal/ui/tui/theme"
)

// detail_pane_people.go is the ONE place a meeting speaker becomes a rendered
// person. The LLM only ever sees an anonymous per-meeting token ("Speaker A");
// names live locally. Every surface — the summary/action prose, the transcript,
// the header roster, the speakers list — resolves a speaker the same way here,
// so a person reads identically (and keeps one identity color) everywhere.
//
// One hue per voice (speakerColorForIndex); confidence is a SEPARATE, visually
// distinct treatment so a guess can never be mistaken for a confirmed name:
//
//	confident (auto/manual)  Alice Nguyen   hue + bold, no marker
//	likely    (pending)      ~Carol?        hue + faint italic, ~…? marker
//	unknown   (unmatched/…)  Speaker A      hue, plain — the anonymous token

type speakerTier int

const (
	tierUnknown   speakerTier = iota // unmatched / unnamed-new → the anonymous token
	tierLikely                       // pending → a marked, dimmed guess
	tierConfident                    // auto/manual → a solid, bold name
)

// resolveSpeaker maps a meeting speaker id to the name to show and how sure we
// are who it is. Confident → the linked person; likely → the top candidate /
// provisional name flagged as a guess; unknown → the anonymous token.
func (d *detailPane) resolveSpeaker(speakerID string) (string, speakerTier) {
	token := d.speakerToken(speakerID)
	mp, ok := d.mappings[speakerID]
	if !ok {
		return token, tierUnknown
	}
	switch mp.MatchStatus {
	case "auto", "manual":
		if mp.ProfileName != "" {
			return mp.ProfileName, tierConfident
		}
	case "pending":
		if mp.ProfileName != "" {
			return mp.ProfileName, tierLikely
		}
		if len(mp.Candidates) > 0 && mp.Candidates[0].DisplayName != "" {
			return mp.Candidates[0].DisplayName, tierLikely
		}
	default: // "new", "unmatched", ""
		// A named provisional profile is still only a guess until confirmed —
		// unless it's named merely by its own anonymous label (an auto-minted
		// "new" stub), which tells us nothing, so it reads as unknown.
		if mp.ProfileName != "" && mp.ProfileName != token {
			return mp.ProfileName, tierLikely
		}
	}
	return token, tierUnknown
}

// speakerToken returns the anonymous per-meeting token for a speaker id, falling
// back to the id when stats aren't computed yet.
func (d *detailPane) speakerToken(speakerID string) string {
	for _, sp := range d.speakers {
		if sp.ID == speakerID {
			if sp.token != "" {
				return sp.token
			}
			return sp.Name
		}
	}
	// Stats not computed yet (speakers declared but no segments) — resolve the
	// per-meeting label off the transcript so we still show "Speaker A", not the
	// bare diarizer id, and resolveSpeaker stays usable on the loading path too.
	for _, sp := range d.transcript.Speakers {
		if sp.ID == speakerID {
			if sp.Label != "" {
				return sp.Label
			}
			if sp.DisplayName != "" {
				return sp.DisplayName
			}
		}
	}
	return speakerID
}

// decorateName adds the confidence marker (a leading ~ and trailing ? for a
// guess) — the text half of the tier treatment, kept separate so callers that
// need to pad/fit a column can measure the plain string before styling.
func decorateName(name string, tier speakerTier) string {
	if tier == tierLikely {
		return "~" + name + "?"
	}
	return name
}

// tierStyle is the color + weight half of the tier treatment, built from the
// speaker's identity hue. The single source of the three-tier look.
func tierStyle(s theme.Styles, colorIdx int, tier speakerTier) lipgloss.Style {
	st := lipgloss.NewStyle().Foreground(speakerColorForIndex(s.T, colorIdx))
	switch tier {
	case tierConfident:
		return st.Bold(true)
	case tierLikely:
		return st.Faint(true).Italic(true)
	default:
		return st
	}
}

// styleSpeaker renders a resolved name at its confidence tier: confident bold,
// likely a faint-italic ~name?, unknown the plain token.
func styleSpeaker(s theme.Styles, name string, colorIdx int, tier speakerTier) string {
	return tierStyle(s, colorIdx, tier).Render(decorateName(name, tier))
}

// speakerSwatch is the small ● identity dot in a speaker's hue, the shared
// anchor used wherever a speaker is named as a chip (roster, list, detail).
func speakerSwatch(s theme.Styles, colorIdx int) string {
	return lipgloss.NewStyle().Foreground(speakerColorForIndex(s.T, colorIdx)).Render("●")
}

// renderPeople rewrites prose (a summary, an action item, an owner) so every
// reference to a meeting speaker renders as that speaker at the right confidence
// tier + color. This is the single path that turns model output into
// properly-named, colored people, locally.
//
// The PRIMARY, reliable handle is the fixed @S<n> token (n = 1-based speaker
// order) that the LLM is instructed to use verbatim — a delimited token can't be
// paraphrased into "the first speaker" or mangled into "speakers A" the way a
// bare label can. As a fallback (legacy summaries, loose output) it also matches
// the resolved person name and the raw transcript label. Longest match first so
// "@S1" beats "@S10"-prefix collisions and "Paulina" beats "Paul"; matching is
// case-insensitive and word-boundaried.
func (d *detailPane) renderPeople(s theme.Styles, text string) string {
	if text == "" {
		return text
	}
	return applyPeople(text, d.peopleReplacer(s))
}

// peopleRepl is one (literal-to-match → already-styled replacement) entry in the
// people-rewrite table, longest match first. lower is match pre-lowercased, so
// applyPeople can cheaply skip an entry the line can't contain.
type peopleRepl struct {
	match  string
	lower  string
	render string
}

// peopleReplacer builds the speaker-rewrite table ONCE: every @S<n> token,
// per-meeting label and resolved name mapped to its styled, colored person. It's
// the expensive half of renderPeople (it resolves + styles every speaker), so a
// hot loop that rewrites many lines — the transcript renders one body per
// segment — builds this once and applies it per line via applyPeople instead of
// rebuilding the identical table for every segment. Returns nil when there's
// nothing to rewrite.
func (d *detailPane) peopleReplacer(s theme.Styles) []peopleRepl {
	if len(d.speakers) == 0 {
		return nil
	}
	// @S<n> tokens are assigned by transcript-speaker order (the SAME order the
	// prompt/seed used), so index that order here to map a token back to a person.
	refByID := make(map[string]string, len(d.transcript.Speakers))
	for i, sp := range d.transcript.Speakers {
		refByID[sp.ID] = fmt.Sprintf("@S%d", i+1)
	}
	var repls []peopleRepl
	seen := map[string]bool{}
	for _, sp := range d.speakers {
		name, tier := d.resolveSpeaker(sp.ID)
		styled := styleSpeaker(s, name, sp.colorIdx, tier)
		for _, m := range []string{refByID[sp.ID], sp.token, name, sp.Name} {
			m = strings.TrimSpace(m)
			// Skip empties, single chars (a bare id like "A" would match the "A"
			// inside a resolved "Speaker A" and double it), and the generic
			// provider ids so we never colorize "spk_0"-style strings in prose.
			if len(m) < 2 || seen[m] || strings.HasPrefix(m, "spk_") || strings.HasPrefix(m, "speaker_") {
				continue
			}
			seen[m] = true
			repls = append(repls, peopleRepl{match: m, lower: strings.ToLower(m), render: styled})
		}
	}
	sort.SliceStable(repls, func(i, j int) bool { return len(repls[i].match) > len(repls[j].match) })
	return repls
}

// applyPeople rewrites text with a prebuilt replacer table (see peopleReplacer).
// Longest match first so "@S1" beats "@S10" and "Paulina" beats "Paul".
//
// The text is lowercased ONCE up front; an entry whose (lowercased) match isn't
// even a substring can't produce a word-boundary match, so it's skipped without
// the per-entry scan + its own ToLower. Most transcript lines mention nobody (or
// one person), so this turns N rescans of every line into one — the dominant
// cost once the replacer itself is no longer rebuilt per segment. The lowercased
// copy is only refreshed after an entry actually rewrites the text, so a name
// introduced by an earlier replacement is still seen by a later entry (the exact
// behaviour of the original sequential passes).
func applyPeople(text string, repls []peopleRepl) string {
	if text == "" || len(repls) == 0 {
		return text
	}
	low := strings.ToLower(text)
	for _, r := range repls {
		if !strings.Contains(low, r.lower) {
			continue
		}
		render := r.render
		next := replaceWordCaseInsensitive(text, r.match, func(string) string { return render })
		if next != text {
			text = next
			low = strings.ToLower(text)
		}
	}
	return text
}
