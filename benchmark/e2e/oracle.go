// Package e2e is the CHAINED benchmark: the full local pipeline
// (LocalSTT → LocalDiarizer → merge) run with each stage fed the *previous
// stage's real output*, scored end-to-end (cpWER / SA-WER + per-stage RTF). This
// is where compounding error shows up — a diarization mistake that cpWER forgives
// but SA-WER penalizes, or a turn boundary that misattributes a correctly-
// recognized word.
//
// The oracle engines below replay ground truth through the real STTEngine /
// SegmentEngine seams. They are concrete (not test fakes) — the second working
// runtime behind the abstraction, proving a runtime is genuinely swappable — and
// they give the chain a deterministic "perfect provider" so the wiring and the
// scorers can be exercised with no models. Feed an oracle perturbed inputs to
// simulate a stage's error and watch it compound downstream.
package e2e

import (
	"context"

	"github.com/lukasstrickler/noto/benchmark/dataset"
	"github.com/lukasstrickler/noto/internal/platform/providers/diarize"
	"github.com/lukasstrickler/noto/internal/platform/providers/stt"
)

// OracleSTT is an STTEngine that replays reference words verbatim (a perfect
// recognizer). Speaker labels are intentionally dropped — a local STT does not
// diarize; the diarizer + merge re-derive them.
type OracleSTT struct{ Words []dataset.Word }

func (o OracleSTT) Name() string { return "oracle" }

func (o OracleSTT) Recognize(context.Context, []byte, stt.TranscribeOptions) ([]stt.EngineWord, error) {
	return oracleWords(o.Words), nil
}

func oracleWords(words []dataset.Word) []stt.EngineWord {
	out := make([]stt.EngineWord, len(words))
	for i, w := range words {
		out[i] = stt.EngineWord{Text: w.Text, StartSeconds: w.StartSeconds, EndSeconds: w.EndSeconds}
	}
	return out
}

// OracleDiarizer is a SegmentEngine that replays reference turns verbatim (a
// perfect diarizer).
type OracleDiarizer struct{ Turns []dataset.Turn }

func (o OracleDiarizer) Name() string { return "oracle" }

func (o OracleDiarizer) Segment(context.Context, []byte, diarize.DiarizeOptions) ([]diarize.EngineTurn, error) {
	return oracleTurns(o.Turns), nil
}

func oracleTurns(turns []dataset.Turn) []diarize.EngineTurn {
	out := make([]diarize.EngineTurn, len(turns))
	for i, t := range turns {
		out[i] = diarize.EngineTurn{Speaker: t.Speaker, StartSeconds: t.StartSeconds, EndSeconds: t.EndSeconds}
	}
	return out
}

var (
	_ stt.STTEngine         = OracleSTT{}
	_ diarize.SegmentEngine = OracleDiarizer{}
)
