package e2e

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lukasstrickler/noto/benchmark/dataset"
	"github.com/lukasstrickler/noto/benchmark/internal/bench"
	"github.com/lukasstrickler/noto/benchmark/metrics"
	"github.com/lukasstrickler/noto/internal/core/artifacts"
	"github.com/lukasstrickler/noto/internal/platform/providers/diarize"
	"github.com/lukasstrickler/noto/internal/platform/providers/merge"
	"github.com/lukasstrickler/noto/internal/platform/providers/stt"
)

// TestSynthAttributedPipeline runs the FULL real local pipeline — STT →
// diarization → merge — on the labeled synthetic meetings and scores every
// stage AND the joint result, so compounding error is visible:
//
//	WER   : transcription alone (words, speaker-agnostic)
//	DER   : diarization alone (who spoke when, overlap-aware)
//	cpWER : the attributed transcript (right words to the right speaker) — the
//	        "speech-to-ownership" metric; cpWER − WER is the attribution tax.
//
// Real engines (no oracle). Env-gated: BENCH_STT_ENGINE + BENCH_DIAR_ENGINE
// (+ their runtime env) and BENCH_SYNTH_DIR (built by
// benchmark/dataset/build_synthetic_meetings.py). The known speaker count is
// passed to the diarizer so cluster-count error doesn't mask attribution error.
func TestSynthAttributedPipeline(t *testing.T) {
	dir := os.Getenv("BENCH_SYNTH_DIR")
	if dir == "" {
		t.Skip("set BENCH_SYNTH_DIR to the synthetic_meetings corpus")
	}
	localSTT, localDiar, ok := buildSynthEngines(t)
	if !ok {
		t.Skip("set BENCH_STT_ENGINE + BENCH_DIAR_ENGINE")
	}
	meta := loadMeetingsMeta(t, dir)
	ctx := context.Background()

	// Honor -hours/-seed: select whole meetings against their declared durations,
	// then run only the chosen ids (same reproducible draw as the other benches).
	skeleton := make([]dataset.Meeting, 0, len(meta))
	for id, mm := range meta {
		skeleton = append(skeleton, dataset.Meeting{ID: id, Duration: mm.Duration})
	}
	sort.Slice(skeleton, func(i, j int) bool { return skeleton[i].ID < skeleton[j].ID })
	selected := bench.Select(t, skeleton)

	t.Logf("%-8s %5s %7s %7s %7s %7s  %s", "meeting", "spk", "WER", "DER", "cpWER", "Δattr", "overlap")
	// Per-meeting scores plus the two stage wall times, so the report can print
	// STT-only and diarization-only speed factors (audio ÷ stage wall) — the
	// answer to "is STT or diar the bottleneck" lives in this split, not in the
	// overlapped end-to-end wall where the longer stage (diar) hides the other.
	type e2eRes struct {
		werE, werR, derE, derR, cpE, cpR float64
		sttWall, diarWall                time.Duration
		audioSec                         float64
	}

	score := func(id string, m dataset.Meeting, tr *artifacts.Transcript, dTurns []diarize.Turn) e2eRes {
		attributed := merge.Attribute(tr, dTurns)
		wer := metrics.WER(m.Reference(), hypAllTokens(tr))
		der := metrics.DER(m.Segments(), turnsToSegments(dTurns), metrics.DefaultDEROptions())
		cp := metrics.CpWER(m.ReferenceBySpeaker(), hypBySpeaker(attributed))

		ovl := overlapSeconds(m.Segments())
		t.Logf("%-8s %5d %6.1f%% %6.1f%% %6.1f%% %+6.1f%%  %.0fs (%.0f%%)",
			id, meta[id].NumSpeakers, wer.Rate*100, der.Rate*100, cp.Rate*100,
			(cp.Rate-wer.Rate)*100, ovl.seconds, ovl.frac*100)

		return e2eRes{
			werE: float64(wer.Errors()), werR: float64(wer.RefLen),
			derE: der.Total(), derR: der.RefSpeech,
			cpE: float64(cp.Errors()), cpR: float64(cp.RefLen),
		}
	}

	var results []e2eRes
	if os.Getenv("BENCH_E2E_BATCH") == "1" {
		batchSize := benchBatchSize(len(selected))
		t.Logf("batch mode: BENCH_E2E_BATCH=1 BENCH_BATCH_SIZE=%d", batchSize)
		for start := 0; start < len(selected); start += batchSize {
			end := start + batchSize
			if end > len(selected) {
				end = len(selected)
			}
			chunk := selected[start:end]
			audios := make([][]byte, len(chunk))
			sttOpts := make([]stt.TranscribeOptions, len(chunk))
			diarOpts := make([]diarize.DiarizeOptions, len(chunk))
			meetings := make([]dataset.Meeting, len(chunk))
			for i, sm := range chunk {
				id := sm.ID
				wav := filepath.Join(dir, id+".wav")
				audio, err := os.ReadFile(wav)
				if err != nil {
					t.Fatalf("read %s: %v", wav, err)
				}
				words, err := dataset.LoadWords(filepath.Join(dir, id+".words.json"))
				if err != nil {
					t.Fatalf("words %s: %v", id, err)
				}
				turns, err := dataset.LoadRTTM(filepath.Join(dir, id+".rttm"))
				if err != nil {
					t.Fatalf("rttm %s: %v", id, err)
				}
				audios[i] = audio
				sttOpts[i] = stt.TranscribeOptions{MeetingID: id, Language: "en"}
				diarOpts[i] = diarize.DiarizeOptions{MeetingID: id, NumSpeakers: meta[id].NumSpeakers}
				meetings[i] = dataset.Meeting{ID: id, Words: words, Turns: turns}
			}

			var (
				trs     []*artifacts.Transcript
				dTurns  [][]diarize.Turn
				sttErr  error
				diarErr error
				wg      sync.WaitGroup
			)
			wg.Add(2)
			go func() {
				defer wg.Done()
				trs, sttErr = localSTT.TranscribeBatch(ctx, audios, sttOpts)
			}()
			go func() {
				defer wg.Done()
				dTurns, diarErr = localDiar.DiarizeBatch(ctx, audios, diarOpts)
			}()
			wg.Wait()
			if sttErr != nil {
				t.Fatalf("batched stt: %v", sttErr)
			}
			if diarErr != nil {
				t.Fatalf("batched diarize: %v", diarErr)
			}
			if len(trs) != len(chunk) || len(dTurns) != len(chunk) {
				t.Fatalf("batch result count mismatch: stt=%d diar=%d meetings=%d", len(trs), len(dTurns), len(chunk))
			}
			for i, sm := range chunk {
				results = append(results, score(sm.ID, meetings[i], trs[i], dTurns[i]))
			}
		}
	} else {
		// Each meeting runs the FULL real chain (STT → diarize → merge) in its own
		// subtest. With -jobs>1 meetings overlap, but a single meeting's stages stay
		// strictly ordered — the chain is never split across the parallel boundary.
		results = bench.RunMeetings(t, selected, func(t *testing.T, sm dataset.Meeting) e2eRes {
			id := sm.ID
			wav := filepath.Join(dir, id+".wav")
			audio, err := os.ReadFile(wav)
			if err != nil {
				t.Fatalf("read %s: %v", wav, err)
			}
			words, err := dataset.LoadWords(filepath.Join(dir, id+".words.json"))
			if err != nil {
				t.Fatalf("words %s: %v", id, err)
			}
			turns, err := dataset.LoadRTTM(filepath.Join(dir, id+".rttm"))
			if err != nil {
				t.Fatalf("rttm %s: %v", id, err)
			}
			m := dataset.Meeting{ID: id, Words: words, Turns: turns}

			// One STT pass + one diarization pass; merge reuses both (the real chain).
			// The two stages are independent (both read the raw audio), so they run
			// concurrently — the same overlap RunChain does — cutting the meeting's
			// wall to ~max(stt, diarize) without touching any score.
			var (
				tr                *artifacts.Transcript
				dTurns            []diarize.Turn
				sttErr            error
				diarErr           error
				sttWall, diarWall time.Duration
				wg                sync.WaitGroup
			)
			wg.Add(2)
			go func() {
				defer wg.Done()
				s0 := time.Now()
				tr, sttErr = localSTT.Transcribe(ctx, audio, stt.TranscribeOptions{MeetingID: id, Language: "en"})
				sttWall = time.Since(s0)
			}()
			go func() {
				defer wg.Done()
				d0 := time.Now()
				dTurns, diarErr = localDiar.Diarize(ctx, audio, diarize.DiarizeOptions{MeetingID: id, NumSpeakers: meta[id].NumSpeakers})
				diarWall = time.Since(d0)
			}()
			wg.Wait()
			if sttErr != nil {
				t.Fatalf("stt %s: %v", id, sttErr)
			}
			if diarErr != nil {
				t.Fatalf("diarize %s: %v", id, diarErr)
			}
			res := score(id, m, tr, dTurns)
			res.sttWall = sttWall
			res.diarWall = diarWall
			res.audioSec = meta[id].Duration
			return res
		})
	}

	var sumWERe, sumWERr, sumDERe, sumDERr, sumCPe, sumCPr float64
	for _, r := range results {
		sumWERe += r.werE
		sumWERr += r.werR
		sumDERe += r.derE
		sumDERr += r.derR
		sumCPe += r.cpE
		sumCPr += r.cpR
	}
	div := func(a, b float64) float64 {
		if b == 0 {
			return 0
		}
		return a / b
	}
	t.Logf("OVERALL  WER %.1f%%  DER %.1f%%  cpWER %.1f%%  (attribution tax %+.1f pts)",
		div(sumWERe, sumWERr)*100, div(sumDERe, sumDERr)*100, div(sumCPe, sumCPr)*100,
		(div(sumCPe, sumCPr)-div(sumWERe, sumWERr))*100)

	// Per-stage speed factors: audio seconds ÷ summed stage wall. These are the
	// numbers comparable to a public STT "speed factor" — and they expose that
	// STT is the fast stage while diarization is the critical path. (mtg00 carries
	// the one-time model load, so it drags both factors below their warm steady
	// state; on a long corpus that cold cost amortizes away.)
	var sumSTT, sumDiar, sumAudio float64
	for _, r := range results {
		sumSTT += r.sttWall.Seconds()
		sumDiar += r.diarWall.Seconds()
		sumAudio += r.audioSec
	}
	if sumSTT > 0 && sumDiar > 0 {
		t.Logf("SPEED    STT %.0fx   DIAR %.0fx   (audio %.0fs ÷ stt %.1fs / diar %.1fs; per-stage, cold load in mtg00)",
			sumAudio/sumSTT, sumAudio/sumDiar, sumAudio, sumSTT, sumDiar)
	}
}

func benchBatchSize(total int) int {
	if total <= 0 {
		return 1
	}
	raw := strings.TrimSpace(os.Getenv("BENCH_BATCH_SIZE"))
	if raw == "" {
		return total
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return total
	}
	if n > total {
		return total
	}
	return n
}

type meetingMeta struct {
	Speakers    []string `json:"speakers"`
	Duration    float64  `json:"duration"`
	NumSpeakers int      `json:"num_speakers"`
}

func loadMeetingsMeta(t *testing.T, dir string) map[string]meetingMeta {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "meetings.json"))
	if err != nil {
		t.Skipf("no meetings.json in %s: %v", dir, err)
	}
	var m map[string]meetingMeta
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("meetings.json: %v", err)
	}
	if len(m) == 0 {
		t.Skip("empty synthetic corpus")
	}
	return m
}

func buildSTTEngine(t *testing.T) (*stt.LocalSTT, bool) {
	t.Helper()
	if os.Getenv("BENCH_STT_ENGINE") == "" {
		return nil, false
	}
	s, err := stt.NewLocalSTTByName(os.Getenv("BENCH_STT_ENGINE"), stt.EngineConfig{ModelDir: os.Getenv("BENCH_STT_MODELS")})
	if err != nil {
		t.Skipf("stt engine: %v", err)
	}
	return s, true
}

func buildDiarEngine(t *testing.T) (*diarize.LocalDiarizer, bool) {
	t.Helper()
	if os.Getenv("BENCH_DIAR_ENGINE") == "" {
		return nil, false
	}
	d, err := diarize.NewLocalDiarizerByName(os.Getenv("BENCH_DIAR_ENGINE"), diarize.EngineConfig{ModelDir: os.Getenv("BENCH_DIAR_MODELS")})
	if err != nil {
		t.Skipf("diar engine: %v", err)
	}
	return d, true
}

func buildSynthEngines(t *testing.T) (*stt.LocalSTT, *diarize.LocalDiarizer, bool) {
	t.Helper()
	s, sok := buildSTTEngine(t)
	d, dok := buildDiarEngine(t)
	return s, d, sok && dok
}

func hypAllTokens(tr *artifacts.Transcript) []string {
	var b strings.Builder
	ws := append([]artifacts.Word(nil), tr.Words...)
	sort.SliceStable(ws, func(i, j int) bool { return ws[i].StartSeconds < ws[j].StartSeconds })
	for _, w := range ws {
		b.WriteString(w.Text)
		b.WriteString(" ")
	}
	return metrics.Normalize(b.String())
}

func hypBySpeaker(tr *artifacts.Transcript) map[string][]string {
	byspk := map[string]string{}
	for _, w := range tr.Words {
		byspk[w.SpeakerID] += " " + w.Text
	}
	out := map[string][]string{}
	for spk, text := range byspk {
		out[spk] = metrics.Normalize(text)
	}
	return out
}

func turnsToSegments(turns []diarize.Turn) []metrics.Segment {
	out := make([]metrics.Segment, len(turns))
	for i, tn := range turns {
		out[i] = metrics.Segment{Speaker: tn.Speaker, Start: tn.StartSeconds, End: tn.EndSeconds}
	}
	return out
}

type overlapStat struct {
	seconds float64
	frac    float64
}

// overlapSeconds measures how much reference speech has >1 speaker active — the
// overlapping-speech portion the pipeline must cope with.
func overlapSeconds(segs []metrics.Segment) overlapStat {
	type ev struct {
		t float64
		d int
	}
	var evs []ev
	var span float64
	for _, s := range segs {
		evs = append(evs, ev{s.Start, 1}, ev{s.End, -1})
		if s.End > span {
			span = s.End
		}
	}
	sort.Slice(evs, func(i, j int) bool { return evs[i].t < evs[j].t })
	var overlap, active float64
	cur := 0
	last := 0.0
	for _, e := range evs {
		if cur > 1 {
			overlap += e.t - last
		}
		if cur > 0 {
			active += e.t - last
		}
		cur += e.d
		last = e.t
	}
	frac := 0.0
	if active > 0 {
		frac = overlap / active
	}
	return overlapStat{seconds: overlap, frac: frac}
}
