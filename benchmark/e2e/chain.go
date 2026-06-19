package e2e

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/lukasstrickler/noto/benchmark/metrics"
	"github.com/lukasstrickler/noto/internal/core/artifacts"
	"github.com/lukasstrickler/noto/internal/platform/providers/diarize"
	"github.com/lukasstrickler/noto/internal/platform/providers/merge"
	"github.com/lukasstrickler/noto/internal/platform/providers/stt"
)

// StageTiming records each chained stage's wall time. RTF turns them into
// real-time factors against the audio length — the speed half of the benchmark,
// localized per stage so a slowdown points at a culprit.
type StageTiming struct {
	STT     time.Duration
	Diarize time.Duration
	Merge   time.Duration
	// Wall is the measured end-to-end time of the chain. STT and diarization run
	// concurrently (both consume the raw audio; only the merge needs both), so
	// Wall ≈ max(STT, Diarize) + Merge — less than the stage sum.
	Wall time.Duration
}

// Total is the end-to-end wall time: the measured chain wall when recorded,
// else the stage sum (for hand-built timings in tests).
func (s StageTiming) Total() time.Duration {
	if s.Wall > 0 {
		return s.Wall
	}
	return s.STT + s.Diarize + s.Merge
}

// RTF reports per-stage and total real-time factors against audioSeconds.
func (s StageTiming) RTF(audioSeconds float64) map[string]float64 {
	rtf := func(d time.Duration) float64 {
		return metrics.Timing{Wall: d, AudioSeconds: audioSeconds}.RTF()
	}
	return map[string]float64{
		"stt":     rtf(s.STT),
		"diarize": rtf(s.Diarize),
		"merge":   rtf(s.Merge),
		"total":   rtf(s.Total()),
	}
}

// RunChain runs the full local pipeline with real handoffs: the STT engine's
// words feed the merge, the diarizer's turns feed the merge, and the merged
// attributed transcript is returned with per-stage timings. This is the
// "upstream" wiring — the opposite of the atomic benches' "golden" inputs.
//
// STT and diarization run CONCURRENTLY: both read only the raw audio, and the
// merge is the first point that needs both. Diarization is the longer stage and
// largely CPU-bound (clustering) while STT leans on the GPU, so overlapping
// them cuts the meeting's wall to ~max(STT, Diarize) for free — the scores are
// untouched because neither stage feeds the other.
func RunChain(
	ctx context.Context,
	audio []byte,
	meetingID string,
	sttEng stt.STTEngine,
	diarEng diarize.SegmentEngine,
) (*artifacts.Transcript, StageTiming, error) {
	localSTT := stt.NewLocalSTT(sttEng)
	localDiar := diarize.NewLocalDiarizer(diarEng)

	var (
		timing  StageTiming
		tr      *artifacts.Transcript
		turns   []diarize.Turn
		sttErr  error
		diarErr error
		wg      sync.WaitGroup
	)
	start := time.Now()
	wg.Add(2)
	go func() {
		defer wg.Done()
		t0 := time.Now()
		tr, sttErr = localSTT.Transcribe(ctx, audio, stt.TranscribeOptions{MeetingID: meetingID})
		timing.STT = time.Since(t0)
	}()
	go func() {
		defer wg.Done()
		t0 := time.Now()
		turns, diarErr = localDiar.Diarize(ctx, audio, diarize.DiarizeOptions{MeetingID: meetingID})
		timing.Diarize = time.Since(t0)
	}()
	wg.Wait()
	if sttErr != nil {
		timing.Wall = time.Since(start)
		return nil, timing, sttErr
	}
	if diarErr != nil {
		timing.Wall = time.Since(start)
		return nil, timing, diarErr
	}

	t2 := time.Now()
	merged := merge.Attribute(tr, turns)
	timing.Merge = time.Since(t2)
	timing.Wall = time.Since(start)

	return merged, timing, nil
}

// RunBatchChain runs several independent meetings through the same chained
// pipeline, but asks batch-capable engines to handle each stage in one warm pass.
// STT batch and diarization batch still run concurrently because both consume
// only raw audio; merge remains per meeting so scores and artifacts stay isolated.
func RunBatchChain(
	ctx context.Context,
	audios [][]byte,
	meetingIDs []string,
	sttEng stt.STTEngine,
	diarEng diarize.SegmentEngine,
	diarOpts []diarize.DiarizeOptions,
) ([]*artifacts.Transcript, []StageTiming, error) {
	return RunBatchLocalChain(ctx, audios, meetingIDs, stt.NewLocalSTT(sttEng), diarize.NewLocalDiarizer(diarEng), diarOpts)
}

// RunBatchLocalChain is RunBatchChain for already-constructed local providers.
func RunBatchLocalChain(
	ctx context.Context,
	audios [][]byte,
	meetingIDs []string,
	localSTT *stt.LocalSTT,
	localDiar *diarize.LocalDiarizer,
	diarOpts []diarize.DiarizeOptions,
) ([]*artifacts.Transcript, []StageTiming, error) {
	if len(meetingIDs) != len(audios) {
		return nil, nil, fmt.Errorf("e2e: RunBatchChain needs one meeting ID per audio (%d audios, %d ids)", len(audios), len(meetingIDs))
	}
	if diarOpts != nil && len(diarOpts) != len(audios) {
		return nil, nil, fmt.Errorf("e2e: RunBatchChain needs one diarization option per audio (%d audios, %d opts)", len(audios), len(diarOpts))
	}

	sttOpts := make([]stt.TranscribeOptions, len(audios))
	dopts := make([]diarize.DiarizeOptions, len(audios))
	for i, id := range meetingIDs {
		sttOpts[i] = stt.TranscribeOptions{MeetingID: id, Language: "en"}
		if diarOpts != nil {
			dopts[i] = diarOpts[i]
		}
		dopts[i].MeetingID = id
	}

	var (
		timings  []StageTiming
		trs      []*artifacts.Transcript
		turns    [][]diarize.Turn
		sttWall  time.Duration
		diarWall time.Duration
		sttErr   error
		diarErr  error
		wg       sync.WaitGroup
	)
	timings = make([]StageTiming, len(audios))
	start := time.Now()
	wg.Add(2)
	go func() {
		defer wg.Done()
		t0 := time.Now()
		trs, sttErr = localSTT.TranscribeBatch(ctx, audios, sttOpts)
		sttWall = time.Since(t0)
	}()
	go func() {
		defer wg.Done()
		t0 := time.Now()
		turns, diarErr = localDiar.DiarizeBatch(ctx, audios, dopts)
		diarWall = time.Since(t0)
	}()
	wg.Wait()
	wall := time.Since(start)
	for i := range timings {
		timings[i].STT = sttWall
		timings[i].Diarize = diarWall
		timings[i].Wall = wall
	}
	if sttErr != nil {
		return nil, timings, sttErr
	}
	if diarErr != nil {
		return nil, timings, diarErr
	}
	if len(trs) != len(audios) || len(turns) != len(audios) {
		return nil, timings, fmt.Errorf("e2e: batch result count mismatch (stt=%d diar=%d audios=%d)", len(trs), len(turns), len(audios))
	}

	out := make([]*artifacts.Transcript, len(audios))
	for i := range audios {
		t0 := time.Now()
		out[i] = merge.Attribute(trs[i], turns[i])
		timings[i].Merge = time.Since(t0)
		if out[i] != nil {
			out[i].MeetingID = meetingIDs[i]
		}
	}
	return out, timings, nil
}

// hypothesisBySpeaker normalizes a merged transcript into the per-speaker token
// shape cpWER / SA-WER consume.
func hypothesisBySpeaker(tr *artifacts.Transcript) map[string][]string {
	raw := merge.WordsBySpeaker(tr)
	out := make(map[string][]string, len(raw))
	for spk, text := range raw {
		if toks := metrics.Normalize(text); len(toks) > 0 {
			out[spk] = toks
		}
	}
	return out
}
