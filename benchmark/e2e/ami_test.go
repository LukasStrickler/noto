package e2e

// AMI is the REAL meeting benchmark (vs the synthetic LibriSpeech corpus). It is
// split into two phases on purpose, so the rented GPU only ever does inference
// (see memory note "benchmark-cost-split"):
//
//	TestAMICapture — runs on the GPU. Reads AMI audio, runs STT ∥ diarization →
//	  merge, and writes one hypothesis JSON per meeting to BENCH_HYP_DIR. No
//	  scoring, no transcript references on the box. The hyps (words + turns, ~KB
//	  each) are streamed to the result volume and fetched back after the box exits.
//
//	TestAMIScore — runs LOCALLY (free). Reads the fetched hyps + the local AMI
//	  references (words/<id>.words.json + identity/ami/<id>.rttm) and computes
//	  WER / DER / cpWER + a per-meeting failure breakdown. Re-runnable for free,
//	  so error analysis never costs GPU time.
//
// The oracle speaker count is read from the (tiny) RTTM on the box — matching the
// synthetic e2e methodology so AMI-vs-synthetic is apples-to-apples.

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
	"github.com/lukasstrickler/noto/benchmark/internal/sample"
	"github.com/lukasstrickler/noto/benchmark/metrics"
	"github.com/lukasstrickler/noto/internal/core/artifacts"
	corebench "github.com/lukasstrickler/noto/internal/core/bench"
	platbench "github.com/lukasstrickler/noto/internal/platform/bench"
	"github.com/lukasstrickler/noto/internal/platform/providers/diarize"
	"github.com/lukasstrickler/noto/internal/platform/providers/merge"
	"github.com/lukasstrickler/noto/internal/platform/providers/stt"
)

func amiAudioDir() string { return filepath.Join("..", "identity", "ami") }
func amiWordsDir() string { return filepath.Join("..", "dataset", "words") }

// hypWord/hypTurn/hypMeeting are the on-the-wire shape streamed back from the GPU.
// Words are the ATTRIBUTED transcript (speaker assigned by merge); turns are the
// raw diarization output. Tiny (text + numbers) — the audio never returns.
type hypWord struct {
	Text    string  `json:"text"`
	Start   float64 `json:"start"`
	End     float64 `json:"end"`
	Speaker string  `json:"speaker"`
	// Confidence is the STT engine's P(correct) for the word when it emits one
	// (NeMo parakeet-server, §B2.6); a pointer so a no-confidence engine stays
	// distinguishable from a genuine 0.0 and the scoring phase skips calibration.
	Confidence *float64 `json:"confidence,omitempty"`
}

type hypTurn struct {
	Speaker string  `json:"speaker"`
	Start   float64 `json:"start"`
	End     float64 `json:"end"`
}

type hypMeeting struct {
	ID          string    `json:"id"`
	Words       []hypWord `json:"words"`
	Turns       []hypTurn `json:"turns"`
	STTms       int64     `json:"stt_ms"`
	DIARms      int64     `json:"diar_ms"`
	AudioSec    float64   `json:"audio_sec"`
	NumSpeakers int       `json:"num_speakers"`
}

// amiMeetings discovers AMI sessions on disk (a wav with a matching rttm) and
// loads the RTTM for the oracle speaker count + a duration estimate (for -hours).
// BENCH_AMI_MEETINGS (comma-separated IDs) restricts the universe — used to keep a
// run to a known set whose (slow, server-side) reference fetch is already done.
func amiMeetings(t *testing.T) []dataset.Meeting {
	t.Helper()
	var allow map[string]struct{}
	if raw := strings.TrimSpace(os.Getenv("BENCH_AMI_MEETINGS")); raw != "" {
		allow = map[string]struct{}{}
		for _, id := range strings.Split(raw, ",") {
			if id = strings.TrimSpace(id); id != "" {
				allow[id] = struct{}{}
			}
		}
	}
	wavs, _ := filepath.Glob(filepath.Join(amiAudioDir(), "*.wav"))
	var meetings []dataset.Meeting
	for _, wav := range wavs {
		id := base(filepath.Base(wav), ".wav")
		if allow != nil {
			if _, ok := allow[id]; !ok {
				continue
			}
		}
		rttm := filepath.Join(amiAudioDir(), id+".rttm")
		turns, err := dataset.LoadRTTM(rttm)
		if err != nil {
			continue // no reference turns → not a scorable meeting
		}
		meetings = append(meetings, dataset.Meeting{ID: id, AudioPath: wav, Turns: turns})
	}
	sort.Slice(meetings, func(i, j int) bool { return meetings[i].ID < meetings[j].ID })
	return bench.Select(t, meetings)
}

// poolEnv reads an integer engine-pool size from env, clamped to [1, capN].
// Empty/invalid falls back to def. Lets the STT and diar pools be sized
// independently of -jobs so resident-model VRAM goes to the bottleneck (diar).
func poolEnv(name string, def, capN int) int {
	n := def
	if raw := strings.TrimSpace(os.Getenv(name)); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			n = v
		}
	}
	if n < 1 {
		n = 1
	}
	if n > capN {
		n = capN
	}
	return n
}

func distinctSpeakers(turns []dataset.Turn) int {
	set := map[string]struct{}{}
	for _, tn := range turns {
		set[tn.Speaker] = struct{}{}
	}
	return len(set)
}

// diarHintSpeakers decides the NumSpeakers passed to the diarizer. The bench
// DEFAULT is the ORACLE count (distinctSpeakers from the reference) — an
// advantage PRODUCTION never has: jobs_pipeline.go passes NumSpeakers:0 because
// the count is unknown at meeting time. Setting BENCH_DIAR_SPEAKERS=auto (or 0)
// runs the product-realistic auto-detect path so the KPIs reflect what a user
// actually gets; any other value keeps the oracle count (preserving the ledger
// baseline). Only the diarizer INPUT changes — the reference/oracle count still
// drives scoring (hypMeeting.NumSpeakers), so WER/DER/cpWER stay comparable.
func diarHintSpeakers(oracle int) int {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("BENCH_DIAR_SPEAKERS"))) {
	case "auto", "0":
		return 0
	default:
		return oracle
	}
}

func turnsEnd(turns []dataset.Turn) float64 {
	var end float64
	for _, tn := range turns {
		if tn.EndSeconds > end {
			end = tn.EndSeconds
		}
	}
	return end
}

// TestAMICapture: GPU phase. Inference only → hyp JSON per meeting.
//
// Meetings run concurrently up to -jobs, each on its OWN warm engine set (one
// Parakeet process + one pyannote process). A single shared server serializes
// every request at its mutex (`e.mu`), so -jobs alone wouldn't overlap anything;
// giving each slot independent processes lets meeting B's GPU embedding run while
// meeting A is in its CPU clustering tail — exactly where a serial single stream
// leaves the card idle (~17% util on real AMI). Scores are unchanged: each
// meeting is still transcribed + diarized on its own; only the GPU packing
// improves. jobs=1 keeps the old single-process single-stream path.
func TestAMICapture(t *testing.T) {
	hypDir := os.Getenv("BENCH_HYP_DIR")
	if hypDir == "" {
		t.Skip("set BENCH_HYP_DIR to capture AMI hypotheses (GPU inference phase)")
	}
	meetings := amiMeetings(t)
	if len(meetings) == 0 {
		t.Skip("no AMI assets (run: python3 benchmark/identity/fetch.py)")
	}
	if err := os.MkdirAll(hypDir, 0o755); err != nil {
		t.Fatalf("mkdir hyp dir: %v", err)
	}

	// STAGE-DECOUPLED warm engine pools (smarter VRAM-vs-compute use). The old
	// design pinned one STT+diar PAIR per in-flight meeting for the WHOLE meeting,
	// so a finished STT model (STT runs ~1.2x faster than diar) and a diar engine
	// in its CPU clustering tail both sat resident-but-idle, blocking VRAM that
	// another meeting could use. Now STT and diar are INDEPENDENT pools: a model is
	// held only while its stage runs, and the two pools are sized separately, so
	// VRAM goes to the bottleneck (diar) instead of split 50/50.
	//
	// `jobs` is the in-flight meeting count (RunMeetings workers). Each stage blocks
	// on its own pool, so BENCH_STT_POOL < jobs time-shares the few, fast STT
	// engines while BENCH_DIAR_POOL ≈ jobs gives every in-flight meeting a diar
	// slot. Both default to jobs (= the old paired behavior, no regression).
	poolN := *sample.Jobs
	if poolN < 1 {
		poolN = 1
	}
	if poolN > len(meetings) {
		poolN = len(meetings)
	}
	sttN := poolEnv("BENCH_STT_POOL", poolN, len(meetings))
	diarN := poolEnv("BENCH_DIAR_POOL", poolN, len(meetings))
	sttPool := make(chan *stt.LocalSTT, sttN)
	for i := 0; i < sttN; i++ {
		s, ok := buildSTTEngine(t)
		if !ok {
			t.Skip("set BENCH_STT_ENGINE")
		}
		sttPool <- s
	}
	diarPool := make(chan *diarize.LocalDiarizer, diarN)
	for i := 0; i < diarN; i++ {
		d, ok := buildDiarEngine(t)
		if !ok {
			t.Skip("set BENCH_DIAR_ENGINE")
		}
		diarPool <- d
	}
	t.Logf("stage pools: STT=%d diar=%d (jobs=%d, meetings=%d)", sttN, diarN, poolN, len(meetings))
	ctx := context.Background()

	type capRow struct {
		id                string
		spk               int
		audioSec          float64
		sttWall, diarWall time.Duration
	}
	rows := bench.RunMeetings(t, meetings, func(t *testing.T, m dataset.Meeting) capRow {
		audio, err := os.ReadFile(m.AudioPath)
		if err != nil {
			t.Fatalf("read %s: %v", m.AudioPath, err)
		}
		numSpk := distinctSpeakers(m.Turns)
		audioSec := turnsEnd(m.Turns)

		var (
			tr                *artifacts.Transcript
			dTurns            []diarize.Turn
			sttErr, diarErr   error
			sttWall, diarWall time.Duration
			wg                sync.WaitGroup
		)
		wg.Add(2)
		go func() {
			defer wg.Done()
			// Hold an STT engine only for the STT phase, then return it — a finished
			// STT no longer blocks the pool for the rest of this meeting's diar.
			es := <-sttPool
			defer func() { sttPool <- es }()
			s0 := time.Now()
			tr, sttErr = es.Transcribe(ctx, audio, stt.TranscribeOptions{MeetingID: m.ID, Language: "en"})
			sttWall = time.Since(s0)
		}()
		go func() {
			defer wg.Done()
			es := <-diarPool
			defer func() { diarPool <- es }()
			d0 := time.Now()
			dTurns, diarErr = es.Diarize(ctx, audio, diarize.DiarizeOptions{MeetingID: m.ID, NumSpeakers: diarHintSpeakers(numSpk)})
			diarWall = time.Since(d0)
		}()
		wg.Wait()
		if sttErr != nil {
			t.Fatalf("stt %s: %v", m.ID, sttErr)
		}
		if diarErr != nil {
			t.Fatalf("diarize %s: %v", m.ID, diarErr)
		}

		attributed := merge.Attribute(tr, dTurns)
		hm := hypMeeting{
			ID: m.ID, AudioSec: audioSec, NumSpeakers: numSpk,
			STTms: sttWall.Milliseconds(), DIARms: diarWall.Milliseconds(),
		}
		for _, w := range attributed.Words {
			hm.Words = append(hm.Words, hypWord{
				Text: w.Text, Start: w.StartSeconds, End: w.EndSeconds,
				Speaker: w.SpeakerID, Confidence: w.Confidence,
			})
		}
		for _, tn := range dTurns {
			hm.Turns = append(hm.Turns, hypTurn{Speaker: tn.Speaker, Start: tn.StartSeconds, End: tn.EndSeconds})
		}
		// Write incrementally so a crash mid-run keeps finished meetings.
		out, _ := json.Marshal(hm)
		if err := os.WriteFile(filepath.Join(hypDir, m.ID+".json"), out, 0o644); err != nil {
			t.Fatalf("write hyp %s: %v", m.ID, err)
		}
		return capRow{id: m.ID, spk: numSpk, audioSec: audioSec, sttWall: sttWall, diarWall: diarWall}
	})

	// Per-meeting walls below are PER-STREAM; with jobs>1 they overlap on the GPU,
	// so they read slower than the true throughput. Aggregate throughput is the
	// command wall (Modal duration_ms) ÷ total audio, not the sum of these.
	t.Logf("%-10s %5s %8s %8s %8s", "meeting", "spk", "audio", "stt", "diar")
	var sumAudio float64
	for _, r := range rows {
		t.Logf("%-10s %5d %7.0fs %7.1fs %7.1fs", r.id, r.spk, r.audioSec, r.sttWall.Seconds(), r.diarWall.Seconds())
		sumAudio += r.audioSec
	}
	t.Logf("CAPTURE wrote %d AMI hyps to %s (jobs=%d, audio %.2f h)", len(rows), hypDir, poolN, sumAudio/3600)
}

// toBenchWords adapts the local hyp wire shape to platform/bench.HypWord so the
// calibration bridge can align+score them. Only the fields the bridge reads
// (text, start, confidence) need carrying.
func toBenchWords(words []hypWord) []platbench.HypWord {
	out := make([]platbench.HypWord, len(words))
	for i, w := range words {
		out[i] = platbench.HypWord{Text: w.Text, Start: w.Start, End: w.End, Speaker: w.Speaker, Confidence: w.Confidence}
	}
	return out
}

func hypWordTokens(words []hypWord) []string {
	ws := append([]hypWord(nil), words...)
	sort.SliceStable(ws, func(i, j int) bool { return ws[i].Start < ws[j].Start })
	var b []string
	for _, w := range ws {
		b = append(b, w.Text)
	}
	return metrics.Normalize(joinSpace(b))
}

func hypWordsBySpeaker(words []hypWord) map[string][]string {
	byspk := map[string][]hypWord{}
	for _, w := range words {
		byspk[w.Speaker] = append(byspk[w.Speaker], w)
	}
	out := map[string][]string{}
	for spk, ws := range byspk {
		sort.SliceStable(ws, func(i, j int) bool { return ws[i].Start < ws[j].Start })
		var b []string
		for _, w := range ws {
			b = append(b, w.Text)
		}
		toks := metrics.Normalize(joinSpace(b))
		if len(toks) > 0 {
			out[spk] = toks
		}
	}
	return out
}

func joinSpace(parts []string) string {
	var b []byte
	for i, p := range parts {
		if i > 0 {
			b = append(b, ' ')
		}
		b = append(b, p...)
	}
	return string(b)
}

func hypTurnsToSegments(turns []hypTurn) []metrics.Segment {
	out := make([]metrics.Segment, len(turns))
	for i, tn := range turns {
		out[i] = metrics.Segment{Speaker: tn.Speaker, Start: tn.Start, End: tn.End}
	}
	return out
}

// TestAMIScore: LOCAL phase. Score fetched hyps against local references. Free.
func TestAMIScore(t *testing.T) {
	hypDir := os.Getenv("BENCH_HYP_DIR")
	if hypDir == "" {
		t.Skip("set BENCH_HYP_DIR to the fetched AMI hypotheses (local scoring phase)")
	}
	hyps, _ := filepath.Glob(filepath.Join(hypDir, "*.json"))
	if len(hyps) == 0 {
		t.Skipf("no hyp JSON in %s", hypDir)
	}

	type row struct {
		id                     string
		werE, werR, derE, derR float64
		cpE, cpR               float64
		sttMS, diarMS          int64
		audioSec, overlapFrac  float64
		hypSpk, refSpk         int
	}
	var rows []row
	var sumSTT, sumDiar, sumAudio float64
	var calWords []corebench.WordConfidence // B6: word confidence ⊗ correctness

	t.Logf("%-10s %5s %7s %7s %7s %7s  %s", "meeting", "spk", "WER", "DER", "cpWER", "Δattr", "overlap")
	for _, hp := range hyps {
		raw, err := os.ReadFile(hp)
		if err != nil {
			t.Fatalf("read hyp %s: %v", hp, err)
		}
		var hm hypMeeting
		if err := json.Unmarshal(raw, &hm); err != nil {
			t.Fatalf("hyp %s: %v", hp, err)
		}
		refWords, err := dataset.LoadWords(filepath.Join(amiWordsDir(), hm.ID+".words.json"))
		if err != nil {
			t.Logf("  %s: no reference words (skip): %v", hm.ID, err)
			continue
		}
		refTurns, err := dataset.LoadRTTM(filepath.Join(amiAudioDir(), hm.ID+".rttm"))
		if err != nil {
			t.Logf("  %s: no reference rttm (skip): %v", hm.ID, err)
			continue
		}
		ref := dataset.Meeting{ID: hm.ID, Words: refWords, Turns: refTurns}

		wer := metrics.WER(ref.Reference(), hypWordTokens(hm.Words))
		// B6 calibration: pair each word's model confidence with whether it matched
		// the reference (same alignment as WER). Contributes nothing for a run whose
		// STT engine emitted no confidence — calibration is then simply skipped.
		calWords = append(calWords, platbench.BuildWordConfidences(ref.Reference(), toBenchWords(hm.Words), "")...)
		der := metrics.DER(ref.Segments(), hypTurnsToSegments(hm.Turns), metrics.DefaultDEROptions())
		cp := metrics.CpWER(ref.ReferenceBySpeaker(), hypWordsBySpeaker(hm.Words))
		ovl := overlapSeconds(ref.Segments())

		t.Logf("%-10s %5d %6.1f%% %6.1f%% %6.1f%% %+6.1f%%  %.0fs (%.0f%%)",
			hm.ID, hm.NumSpeakers, wer.Rate*100, der.Rate*100, cp.Rate*100,
			(cp.Rate-wer.Rate)*100, ovl.seconds, ovl.frac*100)

		rows = append(rows, row{
			id: hm.ID, werE: float64(wer.Errors()), werR: float64(wer.RefLen),
			derE: der.Total(), derR: der.RefSpeech, cpE: float64(cp.Errors()), cpR: float64(cp.RefLen),
			sttMS: hm.STTms, diarMS: hm.DIARms, audioSec: hm.AudioSec, overlapFrac: ovl.frac,
			hypSpk: len(hypWordsBySpeaker(hm.Words)), refSpk: hm.NumSpeakers,
		})
		sumSTT += float64(hm.STTms) / 1000
		sumDiar += float64(hm.DIARms) / 1000
		sumAudio += hm.AudioSec
	}
	if len(rows) == 0 {
		t.Skip("no AMI meetings had both hyps and references (run fetch_ami_words.py)")
	}

	var we, wr, de, dr, ce, cr float64
	for _, r := range rows {
		we += r.werE
		wr += r.werR
		de += r.derE
		dr += r.derR
		ce += r.cpE
		cr += r.cpR
	}
	div := func(a, b float64) float64 {
		if b == 0 {
			return 0
		}
		return a / b
	}
	t.Logf("OVERALL AMI  WER %.1f%%  DER %.1f%%  cpWER %.1f%%  (attribution tax %+.1f pts) over %d meetings / %.2f h",
		div(we, wr)*100, div(de, dr)*100, div(ce, cr)*100, (div(ce, cr)-div(we, wr))*100, len(rows), sumAudio/3600)
	if sumSTT > 0 && sumDiar > 0 {
		t.Logf("SPEED    STT %.0fx   DIAR %.0fx   (audio %.0fs ÷ stt %.1fs / diar %.1fs)",
			sumAudio/sumSTT, sumAudio/sumDiar, sumAudio, sumSTT, sumDiar)
	}

	// B6 calibration snapshot — written only when the STT engine emitted word
	// confidence (calibration.v1 → calibration.json at the run root, beside
	// summary.json). The gate line reports whether the confidence signal beats the
	// random/do-nothing baseline on error capture; repair (B7/B8) stays forbidden
	// until it does, so this is the artifact that unblocks the repair system.
	if len(calWords) > 0 {
		rep := corebench.BuildCalibrationReport(calWords)
		gate := corebench.EvaluateCalibrationGate(rep, 0)
		runDir := filepath.Dir(hypDir) // hypDir is <run_dir>/hyps
		if b, err := json.MarshalIndent(rep, "", "  "); err == nil {
			if err := os.WriteFile(filepath.Join(runDir, "calibration.json"), b, 0o644); err != nil {
				t.Logf("write calibration.json: %v", err)
			}
		}
		t.Logf("CALIBRATION words=%d errors=%d  ECE %.3f  Brier %.3f  bottom-decile capture %.3f (baseline %.2f, lift %+.3f)  high-conf-err %.3f  admissible=%v",
			rep.Words, rep.Errors, rep.ECEBySlice["default"], rep.BrierBySlice["default"],
			rep.BottomDecileCapture, gate.RandomBaseline, gate.CaptureLiftOverRandom,
			rep.HighConfErrorRate, gate.SignalAdmissible)
	} else {
		t.Logf("CALIBRATION skipped — no word confidence on these hyps (STT engine emitted none)")
	}

	// Failure analysis: worst meetings by cpWER, and speaker-count miscounts.
	sort.Slice(rows, func(i, j int) bool { return div(rows[i].cpE, rows[i].cpR) > div(rows[j].cpE, rows[j].cpR) })
	t.Logf("WORST cpWER:")
	for i, r := range rows {
		if i >= 3 {
			break
		}
		t.Logf("  %-10s cpWER %.1f%%  DER %.1f%%  spk hyp %d / ref %d  overlap %.0f%%",
			r.id, div(r.cpE, r.cpR)*100, div(r.derE, r.derR)*100, r.hypSpk, r.refSpk, r.overlapFrac*100)
	}
}

func base(name, suffix string) string {
	if len(name) >= len(suffix) && name[len(name)-len(suffix):] == suffix {
		return name[:len(name)-len(suffix)]
	}
	return name
}
