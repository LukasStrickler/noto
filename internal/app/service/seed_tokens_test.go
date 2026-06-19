package service

import (
	"regexp"
	"strings"
	"testing"
)

// Seed artifacts must reference participants by their anonymous per-meeting token
// ("Speaker A"), never a bare first name — so the UI resolves each token to a
// person + identity color locally and renders consistently across every tab
// (summary, decisions, actions, risks, transcript). This guards against a
// regression back to hardcoded names like "Bob" / "Marc" in the fixtures.
func TestSeedArtifactsReferenceSpeakersByToken(t *testing.T) {
	for _, fx := range seedFixtures {
		validToken := map[string]bool{}
		for _, tok := range seedSpeakerTokens(fx) {
			validToken[tok] = true
		}

		sum := buildSeedSummary(fx.ID, fx)
		tr := buildSeedTranscript(fx.ID, fx)
		md := renderSeedSummaryMD(fx)

		// Action owners must be a token (or empty), so they resolve to a person.
		for _, a := range sum.ActionItems {
			if a.Owner != "" && !validToken[a.Owner] {
				t.Errorf("%s: action owner %q must be a per-meeting speaker token", fx.Title, a.Owner)
			}
		}

		// No unresolved {spk_*} placeholder may survive into stored artifacts.
		blobs := []string{sum.ShortSummary, md}
		for _, d := range sum.Decisions {
			blobs = append(blobs, d.Text)
		}
		for _, r := range sum.Risks {
			blobs = append(blobs, r.Text)
		}
		for _, q := range sum.OpenQuestions {
			blobs = append(blobs, q.Text)
		}
		for _, s := range tr.Segments {
			blobs = append(blobs, s.Text)
		}
		for _, b := range blobs {
			if strings.Contains(b, "{spk_") {
				t.Errorf("%s: unresolved speaker placeholder in %q", fx.Title, b)
			}
		}
	}
}

// underscoreIdent matches a snake_case code identifier ("summary_body") embedded
// in prose. Stored seed artifacts hold no such ids — the speaker token is "@S<n>"
// and segment ids never appear in text — so any hit is unnatural jargon that
// renders an out-of-place underscore in the UI.
var underscoreIdent = regexp.MustCompile(`[A-Za-z0-9]+_[A-Za-z0-9]+`)

// The seed reads like a real meeting, so its prose must not carry snake_case
// code identifiers (e.g. "summary_body") that surface as stray underscores in the
// summary, items and transcript tabs. Guards against re-introducing jargon ids.
func TestSeedProseHasNoUnderscoreIdentifiers(t *testing.T) {
	for _, fx := range seedFixtures {
		sum := buildSeedSummary(fx.ID, fx)
		tr := buildSeedTranscript(fx.ID, fx)

		blobs := []string{sum.ShortSummary, renderSeedSummaryMD(fx)}
		for _, d := range sum.Decisions {
			blobs = append(blobs, d.Text)
		}
		for _, a := range sum.ActionItems {
			blobs = append(blobs, a.Text)
		}
		for _, r := range sum.Risks {
			blobs = append(blobs, r.Text)
		}
		for _, q := range sum.OpenQuestions {
			blobs = append(blobs, q.Text)
		}
		for _, s := range tr.Segments {
			blobs = append(blobs, s.Text)
		}
		for _, b := range blobs {
			if m := underscoreIdent.FindString(b); m != "" {
				t.Errorf("%s: prose contains snake_case identifier %q (reads as a stray underscore):\n  %s", fx.Title, m, b)
			}
		}
	}
}
