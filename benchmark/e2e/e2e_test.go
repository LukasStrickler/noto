package e2e

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/lukasstrickler/noto/benchmark/dataset"
	"github.com/lukasstrickler/noto/benchmark/internal/bench"
	"github.com/lukasstrickler/noto/benchmark/metrics"
	"github.com/lukasstrickler/noto/internal/core/artifacts"
	"github.com/lukasstrickler/noto/internal/platform/providers/diarize"
	"github.com/lukasstrickler/noto/internal/platform/providers/stt"
)

// a small, internally-consistent two-speaker meeting (words + turns agree), so a
// perfect chain scores zero and any injected error is attributable.
func fixture() dataset.Meeting {
	return dataset.Meeting{
		ID: "fix",
		Words: []dataset.Word{
			{Speaker: "A", StartSeconds: 0, EndSeconds: 1, Text: "hello"},
			{Speaker: "A", StartSeconds: 1, EndSeconds: 2, Text: "there"},
			{Speaker: "B", StartSeconds: 6, EndSeconds: 7, Text: "general"},
			{Speaker: "B", StartSeconds: 7, EndSeconds: 8, Text: "kenobi"},
		},
		Turns: []dataset.Turn{
			{Speaker: "A", StartSeconds: 0, EndSeconds: 5},
			{Speaker: "B", StartSeconds: 5, EndSeconds: 10},
		},
	}
}

func runChain(t *testing.T, words []dataset.Word, turns []dataset.Turn) *artifacts.Transcript {
	t.Helper()
	merged, timing, err := RunChain(context.Background(), nil, "fix", OracleSTT{Words: words}, OracleDiarizer{Turns: turns})
	if err != nil {
		t.Fatalf("RunChain: %v", err)
	}
	// timing/RTF structure must be present (values ~0 for the instant oracle).
	if _, ok := timing.RTF(8)["total"]; !ok {
		t.Error("RTF missing total")
	}
	return merged
}

func identityMapping(m dataset.Meeting) map[string]string {
	mp := map[string]string{}
	for spk := range m.ReferenceBySpeaker() {
		mp[spk] = spk
	}
	return mp
}

func TestChainPerfect(t *testing.T) {
	m := fixture()
	merged := runChain(t, m.Words, m.Turns)
	hyp := hypothesisBySpeaker(merged)

	cp := metrics.CpWER(m.ReferenceBySpeaker(), hyp)
	if cp.Rate != 0 {
		t.Errorf("perfect cpWER = %v, want 0", cp.Rate)
	}
	sa := metrics.SAWER(m.ReferenceBySpeaker(), hyp, identityMapping(m))
	if sa.Rate != 0 {
		t.Errorf("perfect SA-WER = %v, want 0", sa.Rate)
	}
}

// A diarizer that swaps the two speakers' labels: cpWER forgives the permutation
// (who-said-what content is intact), but SA-WER under the fixed identity mapping
// catches that every word landed on the wrong person.
func TestChainDiarizationLabelSwap(t *testing.T) {
	m := fixture()
	swapped := []dataset.Turn{
		{Speaker: "B", StartSeconds: 0, EndSeconds: 5},
		{Speaker: "A", StartSeconds: 5, EndSeconds: 10},
	}
	merged := runChain(t, m.Words, swapped)
	hyp := hypothesisBySpeaker(merged)

	if cp := metrics.CpWER(m.ReferenceBySpeaker(), hyp); cp.Rate != 0 {
		t.Errorf("label-swap cpWER = %v, want 0 (permutation forgiven)", cp.Rate)
	}
	if sa := metrics.SAWER(m.ReferenceBySpeaker(), hyp, identityMapping(m)); sa.Rate != 1.0 {
		t.Errorf("label-swap SA-WER = %v, want 1.0 (attribution wrong)", sa.Rate)
	}
}

// A diarizer whose A/B boundary is too early misattributes a correctly-recognized
// word — STT is perfect, yet cpWER is nonzero. This is the compounding the e2e
// layer exists to surface.
func TestChainDiarizationBoundaryCompounds(t *testing.T) {
	m := fixture()
	earlyBoundary := []dataset.Turn{
		{Speaker: "A", StartSeconds: 0, EndSeconds: 1.2}, // "there" (mid 1.5) now falls in B
		{Speaker: "B", StartSeconds: 1.2, EndSeconds: 10},
	}
	merged := runChain(t, m.Words, earlyBoundary)
	hyp := hypothesisBySpeaker(merged)

	cp := metrics.CpWER(m.ReferenceBySpeaker(), hyp)
	// A: [hello] vs [hello,there] = 1 del; B: [there,general,kenobi] vs
	// [general,kenobi] = 1 ins → 2 errors over 4 ref tokens.
	if cp.Errors() != 2 || cp.RefLen != 4 {
		t.Errorf("boundary cpWER = %+v, want 2 errors / 4 ref", cp)
	}
	if math.Abs(cp.Rate-0.5) > 1e-9 {
		t.Errorf("boundary cpWER rate = %v, want 0.5", cp.Rate)
	}
}

// A recognition error (one substituted word) with perfect diarization is a clean
// 1/N cpWER — the STT stage's error in isolation, measured through the chain.
func TestChainTranscriptionErrorCompounds(t *testing.T) {
	m := fixture()
	wrong := append([]dataset.Word(nil), m.Words...)
	wrong[3].Text = "grievous" // "kenobi" → "grievous"
	merged := runChain(t, wrong, m.Turns)
	hyp := hypothesisBySpeaker(merged)

	cp := metrics.CpWER(m.ReferenceBySpeaker(), hyp)
	if math.Abs(cp.Rate-0.25) > 1e-9 {
		t.Errorf("one-sub cpWER = %v, want 0.25", cp.Rate)
	}
}

type batchOracleSTT struct {
	words [][]dataset.Word
	calls int
}

func (b *batchOracleSTT) Name() string { return "batch-oracle" }

func (b *batchOracleSTT) Recognize(_ context.Context, _ []byte, opts stt.TranscribeOptions) ([]stt.EngineWord, error) {
	for i, words := range b.words {
		if opts.MeetingID == string(rune('a'+i)) {
			return oracleWords(words), nil
		}
	}
	return nil, nil
}

func (b *batchOracleSTT) RecognizeBatch(_ context.Context, audios [][]byte, _ stt.TranscribeOptions) ([][]stt.EngineWord, error) {
	b.calls++
	out := make([][]stt.EngineWord, len(audios))
	for i := range audios {
		out[i] = oracleWords(b.words[i])
	}
	return out, nil
}

type batchOracleDiarizer struct {
	turns [][]dataset.Turn
	calls int
}

func (b *batchOracleDiarizer) Name() string { return "batch-oracle" }

func (b *batchOracleDiarizer) Segment(_ context.Context, _ []byte, opts diarize.DiarizeOptions) ([]diarize.EngineTurn, error) {
	for i, turns := range b.turns {
		if opts.MeetingID == string(rune('a'+i)) {
			return oracleTurns(turns), nil
		}
	}
	return nil, nil
}

func (b *batchOracleDiarizer) SegmentBatch(_ context.Context, audios [][]byte, _ []diarize.DiarizeOptions) ([][]diarize.EngineTurn, error) {
	b.calls++
	out := make([][]diarize.EngineTurn, len(audios))
	for i := range audios {
		out[i] = oracleTurns(b.turns[i])
	}
	return out, nil
}

func TestBatchChainPerfectMatchesPerMeetingChain(t *testing.T) {
	m0 := fixture()
	m0.ID = "a"
	m1 := fixture()
	m1.ID = "b"
	m1.Words[0].Text = "second"
	meetings := []dataset.Meeting{m0, m1}
	audios := [][]byte{[]byte("a"), []byte("b")}

	sttEng := &batchOracleSTT{words: [][]dataset.Word{m0.Words, m1.Words}}
	diarEng := &batchOracleDiarizer{turns: [][]dataset.Turn{m0.Turns, m1.Turns}}
	merged, timings, err := RunBatchChain(context.Background(), audios, []string{"a", "b"}, sttEng, diarEng, nil)
	if err != nil {
		t.Fatalf("RunBatchChain: %v", err)
	}
	if sttEng.calls != 1 || diarEng.calls != 1 {
		t.Fatalf("batch calls stt=%d diar=%d, want 1/1", sttEng.calls, diarEng.calls)
	}
	if len(merged) != 2 || len(timings) != 2 {
		t.Fatalf("results = %d timings = %d, want 2/2", len(merged), len(timings))
	}
	for i, tr := range merged {
		cp := metrics.CpWER(meetings[i].ReferenceBySpeaker(), hypothesisBySpeaker(tr))
		if cp.Rate != 0 {
			t.Fatalf("meeting %d cpWER = %v, want 0", i, cp.Rate)
		}
		if tr.MeetingID != meetings[i].ID {
			t.Fatalf("meeting id = %q, want %q", tr.MeetingID, meetings[i].ID)
		}
	}
}

// TestE2EAMI runs the oracle chain over real AMI meetings when both audio and
// reference words are present — a real-data wiring check (perfect by construction)
// and the slot where real local engines later replace the oracles. It t.Skips
// without assets.
func TestE2EAMI(t *testing.T) {
	meetings := loadAMIMeetingsWithWords(t)
	if len(meetings) == 0 {
		t.Skip("no AMI meetings with reference words present")
	}
	t.Logf("%-10s %8s %8s", "meeting", "cpWER", "SA-WER")
	for _, m := range meetings {
		merged := runChain(t, m.Words, m.Turns)
		hyp := hypothesisBySpeaker(merged)
		cp := metrics.CpWER(m.ReferenceBySpeaker(), hyp)
		sa := metrics.SAWER(m.ReferenceBySpeaker(), hyp, identityMapping(m))
		t.Logf("%-10s %7.1f%% %7.1f%%", m.ID, cp.Rate*100, sa.Rate*100)
	}
}

func loadAMIMeetingsWithWords(t *testing.T) []dataset.Meeting {
	t.Helper()
	wordsDir := filepath.Join("..", "dataset", "words")
	amiDir := filepath.Join("..", "identity", "ami")
	refs, _ := filepath.Glob(filepath.Join(wordsDir, "*.words.json"))
	var out []dataset.Meeting
	for _, r := range refs {
		id := filepath.Base(r)
		id = id[:len(id)-len(".words.json")]
		rttm := filepath.Join(amiDir, id+".rttm")
		if _, err := os.Stat(rttm); err != nil {
			continue
		}
		words, err := dataset.LoadWords(r)
		if err != nil {
			t.Fatalf("LoadWords %s: %v", r, err)
		}
		turns, err := dataset.LoadRTTM(rttm)
		if err != nil {
			t.Fatalf("LoadRTTM %s: %v", rttm, err)
		}
		out = append(out, dataset.Meeting{ID: id, Words: words, Turns: turns})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return bench.Select(t, out)
}
