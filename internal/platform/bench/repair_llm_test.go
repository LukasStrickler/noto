package bench

import (
	"context"
	"errors"
	"testing"

	corebench "github.com/lukasstrickler/noto/internal/core/bench"
)

// fakeCorrector drives the context-correction loop deterministically without a network
// call. reply maps a request to the corrected span text; it records the last request so
// tests can assert the context was assembled correctly.
type fakeCorrector struct {
	reply func(CorrectionRequest) string
	last  CorrectionRequest
	err   error
}

func (f *fakeCorrector) Correct(_ context.Context, req CorrectionRequest) (string, error) {
	f.last = req
	if f.err != nil {
		return "", f.err
	}
	return f.reply(req), nil
}

func newTestCorrectorReDecoder(words map[string][]HypWord, c SpanCorrector) *correctorReDecoder {
	return &correctorReDecoder{
		ctx:         context.Background(),
		words:       words,
		corrector:   c,
		contextSec:  llmContextSec,
		costPerSpan: llmCostPerCallUSD,
	}
}

func TestSpreadWords_SpreadsTokensWithinSpan(t *testing.T) {
	got := spreadWords("quick brown fox", 1, 4)
	if len(got) != 3 {
		t.Fatalf("expected 3 words, got %d", len(got))
	}
	if spreadWords("", 1, 4) != nil {
		t.Errorf("empty text must yield no words")
	}
	for i, w := range got {
		mid := (w.Start + w.End) / 2
		if mid < 1 || mid >= 4 {
			t.Errorf("word %d (%q) midpoint %v escaped the span [1,4)", i, w.Text, mid)
		}
		if i > 0 && w.Start < got[i-1].Start {
			t.Errorf("words must be time-ordered")
		}
	}
}

func TestCorrectorReDecode_BuildsContextAndMapsCorrection(t *testing.T) {
	words := map[string][]HypWord{"M": attemptHyp([2]string{"kwik", "braun"}).Words}
	fc := &fakeCorrector{reply: func(CorrectionRequest) string { return "quick brown" }}
	dec := newTestCorrectorReDecoder(words, fc)

	repl, cost, err := dec.ReDecode("M", 1, 3, corebench.MethodContextBiased)
	if err != nil {
		t.Fatalf("ReDecode: %v", err)
	}
	// Context is assembled from the BASELINE words around the span.
	if fc.last.SpanText != "kwik braun" {
		t.Errorf("span text = %q, want %q", fc.last.SpanText, "kwik braun")
	}
	if fc.last.Before != "the" || fc.last.After != "fox" {
		t.Errorf("context wrong: before=%q after=%q", fc.last.Before, fc.last.After)
	}
	if len(repl) != 2 || repl[0].Text != "quick" || repl[1].Text != "brown" {
		t.Errorf("correction not mapped to words: %+v", repl)
	}
	if cost != llmCostPerCallUSD {
		t.Errorf("cost = %v, want %v", cost, llmCostPerCallUSD)
	}
}

func TestCorrectorReDecode_UnknownMeetingErrors(t *testing.T) {
	dec := newTestCorrectorReDecoder(map[string][]HypWord{}, &fakeCorrector{reply: func(CorrectionRequest) string { return "x" }})
	if _, _, err := dec.ReDecode("MISSING", 0, 1, corebench.MethodContextBiased); err == nil {
		t.Error("expected an error for an unknown meeting")
	}
}

// End-to-end through the validated attempt machine: a corrector that fixes the wrong
// low-confidence words is accepted and lowers benchmark WER — exactly like the oracle
// re-decode test, but driven by the LLM-correction source.
func TestAttemptMeeting_CorrectorFixIsAcceptedAndImprovesWER(t *testing.T) {
	ref := spliceRef() // "the quick brown fox"
	hyp := attemptHyp([2]string{"kwik", "braun"})
	words := map[string][]HypWord{"M": hyp.Words}
	dec := newTestCorrectorReDecoder(words, &fakeCorrector{reply: func(CorrectionRequest) string { return "quick brown" }})

	rep, spans, differed, attempted := attemptMeeting(ref, hyp, dec, 0.5, corebench.MethodContextBiased)
	if !attempted || spans != 1 || differed != 1 {
		t.Fatalf("expected 1 attempted/differed span, got attempted=%v spans=%d differed=%d", attempted, spans, differed)
	}
	if rep.AcceptedRepairs != 1 || rep.NegativeRepairs != 0 {
		t.Errorf("a correct fix should be accepted: %+v", rep)
	}
	if rep.NetWERDelta >= 0 {
		t.Errorf("accepted correction must lower benchmark WER, got %v", rep.NetWERDelta)
	}
}

// A corrector that echoes the (wrong) span text proposes nothing new — the loop must see
// no difference and accept nothing, never fabricate an improvement.
func TestAttemptMeeting_CorrectorNoOpWashes(t *testing.T) {
	ref := spliceRef()
	hyp := attemptHyp([2]string{"kwik", "braun"})
	words := map[string][]HypWord{"M": hyp.Words}
	dec := newTestCorrectorReDecoder(words, &fakeCorrector{reply: func(req CorrectionRequest) string { return req.SpanText }})

	rep, _, differed, attempted := attemptMeeting(ref, hyp, dec, 0.5, corebench.MethodContextBiased)
	if !attempted {
		t.Fatal("span should have been attempted")
	}
	if differed != 0 {
		t.Errorf("an echo correction produces no different words, want differed=0 got %d", differed)
	}
	if rep.AcceptedRepairs != 0 {
		t.Errorf("a no-op correction must not be accepted: %+v", rep)
	}
}

func TestAttemptMeeting_CorrectorErrorSkipsCleanly(t *testing.T) {
	ref := spliceRef()
	hyp := attemptHyp([2]string{"kwik", "braun"})
	words := map[string][]HypWord{"M": hyp.Words}
	dec := newTestCorrectorReDecoder(words, &fakeCorrector{err: errors.New("llm down"), reply: func(CorrectionRequest) string { return "" }})

	rep, _, _, attempted := attemptMeeting(ref, hyp, dec, 0.5, corebench.MethodContextBiased)
	if !attempted {
		t.Fatal("plan had a candidate span")
	}
	if rep.AcceptedRepairs != 0 || rep.NegativeRepairs != 0 {
		t.Errorf("a failed correction must yield no outcomes: %+v", rep)
	}
}
