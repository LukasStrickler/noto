package bench

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	corebench "github.com/lukasstrickler/noto/internal/core/bench"
)

// repair_llm.go is the context/LLM-correction repair source (§10.5, the
// MethodContextBiased method) — the one genuinely-DIFFERENT information source from the
// acoustic models. The validated finding from fp32/beam/1.1b/perturbation is that
// same-FAMILY re-decodes can't fix the hard bottom-decile spans (they're acoustically
// hard for any acoustic model of that family). A language model brings a different
// signal entirely: priors over the surrounding text, grammar, and entities — so it can
// repair context-predictable errors (homophones, agreement, glossary names) that an
// acoustic re-decode cannot, while contributing nothing on purely-acoustic confusions.
// Whether that nets positive is an EMPIRICAL question answered by the SAME B7 machine
// that gated every other source: this is just another ReDecoder, measured on the
// reference, with the gate refusing any net-negative result. It needs NO new GPU — it
// runs over an existing run's hyps + a text-only LLM call.
//
// The acoustic seam is kept pure and offline-testable: the network lives entirely behind
// the injected SpanCorrector, and the timestamp mapping (spreadWords) + context slicing
// are deterministic. A fake corrector drives the whole loop in tests.

const (
	// llmContextSec is how many seconds of surrounding baseline transcript are handed to
	// the corrector on each side of a span — enough local context to disambiguate without
	// flooding the prompt (and the cost) with the whole meeting.
	llmContextSec = 8.0
	// llmCostPerCallUSD is the flat marginal cost charged per corrected span. An LLM
	// correction of a few-second span is a small, bounded text completion; this keeps the
	// gate's accepted-per-USD honest without pretending sub-cent precision we don't have.
	llmCostPerCallUSD = 0.0002
)

// CorrectionRequest is one low-confidence span handed to a SpanCorrector: the current
// (low-confidence) hypothesis text for the span, plus the baseline transcript text
// immediately before and after it for context. A corrector returns its best-guess
// corrected text for SpanText alone (Before/After are context, never rewritten).
type CorrectionRequest struct {
	MeetingID string
	Before    string
	SpanText  string
	After     string
}

// SpanCorrector proposes a corrected transcription for a span's text given its context.
// The real implementation calls an LLM (text-only, no audio); a fake drives tests. It is
// the single network seam of the context-correction repair source — the analog of
// ReDecoder's GPU seam, but cheaper and offline.
type SpanCorrector interface {
	Correct(ctx context.Context, req CorrectionRequest) (text string, err error)
}

// correctorReDecoder adapts a SpanCorrector to the ReDecoder seam so the context-
// correction source reuses the whole validated attempt+measure+gate loop. Per span it
// builds the surrounding context from the BASELINE words, asks the corrector for a fix,
// and maps the returned text back onto timestamps spread evenly across the span. WER is
// text-based and the splice decides span membership by word midpoint, so spreading the
// corrected tokens across [start,end] keeps them in-span without needing real alignment.
type correctorReDecoder struct {
	ctx        context.Context
	words      map[string][]HypWord
	corrector  SpanCorrector
	contextSec float64
	costPerSpan float64
}

// newCorrectorReDecoder loads the BASELINE run's words (the run being repaired supplies
// its own context) keyed by meeting, so each correction is a pure lookup + slice + call.
func newCorrectorReDecoder(ctx context.Context, baselineDir string, c SpanCorrector) (*correctorReDecoder, error) {
	hyps, err := loadMeetingHyps(filepath.Join(baselineDir, "hyps"))
	if err != nil {
		return nil, err
	}
	words := make(map[string][]HypWord, len(hyps))
	for _, h := range hyps {
		if k := h.Key(); k != "" {
			words[k] = h.Words
		}
	}
	return &correctorReDecoder{ctx: ctx, words: words, corrector: c, contextSec: llmContextSec, costPerSpan: llmCostPerCallUSD}, nil
}

// ReDecode satisfies ReDecoder: build the span's context from the baseline, correct it,
// and return the corrected words spread across the span. An unknown meeting is an error
// the loop skips cleanly; an empty correction returns no words (a wash, never a crash).
func (d *correctorReDecoder) ReDecode(meetingID string, startSec, endSec float64, _ corebench.RepairMethod) ([]HypWord, float64, error) {
	ws, ok := d.words[meetingID]
	if !ok {
		return nil, 0, fmt.Errorf("baseline run has no decode for meeting %q", meetingID)
	}
	req := CorrectionRequest{
		MeetingID: meetingID,
		Before:    joinWords(hypWordsInSpan(ws, startSec-d.contextSec, startSec)),
		SpanText:  joinWords(hypWordsInSpan(ws, startSec, endSec)),
		After:     joinWords(hypWordsInSpan(ws, endSec, endSec+d.contextSec)),
	}
	corrected, err := d.corrector.Correct(d.ctx, req)
	if err != nil {
		return nil, 0, err
	}
	return spreadWords(corrected, startSec, endSec), d.costPerSpan, nil
}

// AttemptRepairsWithCorrector runs the B7 attempt+measure loop using a SpanCorrector
// (LLM/context correction, MethodContextBiased) as the repair source. Context comes from
// the baseline run itself, so there is no alternate run — just runID + the corrector.
func (r *Runner) AttemptRepairsWithCorrector(ctx context.Context, runID string, c SpanCorrector, threshold float64) (RepairAttemptResult, error) {
	dec, err := newCorrectorReDecoder(ctx, r.Store.RunDir(runID), c)
	if err != nil {
		return RepairAttemptResult{RunID: runID, Method: string(corebench.MethodContextBiased)}, err
	}
	return r.AttemptRepairs(runID, dec, threshold, corebench.MethodContextBiased)
}

// joinWords renders a word slice as space-joined text — the corrector speaks text, not
// timestamps.
func joinWords(ws []HypWord) string {
	parts := make([]string, len(ws))
	for i, w := range ws {
		parts[i] = w.Text
	}
	return strings.Join(parts, " ")
}

// spreadWords maps a corrected text string onto HypWords with timestamps spread evenly
// across [startSec,endSec). Within-span timing precision is irrelevant to WER and only
// needs to keep every token's midpoint inside the span (so the splice replaces exactly
// the right baseline words); even spacing guarantees that. Empty text yields no words.
func spreadWords(text string, startSec, endSec float64) []HypWord {
	toks := strings.Fields(text)
	if len(toks) == 0 {
		return nil
	}
	span := endSec - startSec
	if span <= 0 {
		span = 0
	}
	step := span / float64(len(toks))
	out := make([]HypWord, len(toks))
	for i, t := range toks {
		s := startSec + step*float64(i)
		out[i] = HypWord{Text: t, Start: s, End: s + step}
	}
	return out
}
