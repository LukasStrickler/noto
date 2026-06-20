package bench_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lukasstrickler/noto/internal/platform/bench"
)

func TestScoreMeetingHyps_ScoresWERDERCPWER(t *testing.T) {
	root := t.TempDir()
	wordsDir := filepath.Join(root, "words")
	rttmDir := filepath.Join(root, "rttm")
	if err := os.MkdirAll(wordsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(rttmDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wordsDir, "m1.words.json"), []byte(`[
{"speaker":"A","start":0.0,"end":0.4,"text":"hello"},
{"speaker":"A","start":0.5,"end":0.9,"text":"world"},
{"speaker":"B","start":1.0,"end":1.4,"text":"ship"}
]`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rttmDir, "m1.rttm"), []byte("SPEAKER m1 1 0.000 1.000 <NA> <NA> A <NA> <NA>\nSPEAKER m1 1 1.000 0.500 <NA> <NA> B <NA> <NA>\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	metricsFile, err := bench.ScoreMeetingHyps("run_score_test", []bench.MeetingHyp{{
		MeetingID: "m1",
		AudioSec:  120,
		SpeechSec: 90,
		Words: []bench.HypWord{
			{Text: "hello", Start: 0.0, End: 0.4, Speaker: "A"},
			{Text: "word", Start: 0.5, End: 0.9, Speaker: "A"},
			{Text: "ship", Start: 1.0, End: 1.4, Speaker: "B"},
		},
		Turns: []bench.HypTurn{
			{Speaker: "A", Start: 0.0, End: 1.0},
			{Speaker: "B", Start: 1.0, End: 1.5},
		},
	}}, bench.ScoreOptions{WordsDir: wordsDir, RTTMDir: rttmDir})
	if err != nil {
		t.Fatal(err)
	}
	if got := metricsFile.Aggregate["wer"]; got != 1.0/3.0 {
		t.Fatalf("wer=%v", got)
	}
	if got := metricsFile.Aggregate["der"]; got != 0 {
		t.Fatalf("der=%v", got)
	}
	if got := metricsFile.Aggregate["cpwer"]; got != 1.0/3.0 {
		t.Fatalf("cpwer=%v", got)
	}
	if got := metricsFile.Denominators["meetings_scored"]; got != float64(1) {
		t.Fatalf("meetings_scored=%v", got)
	}
}

func TestScoreMeetingHyps_MarksUnscoredWhenRefsMissing(t *testing.T) {
	metricsFile, err := bench.ScoreMeetingHyps("run_unscored_test", []bench.MeetingHyp{{
		MeetingID: "m1",
		AudioSec:  60,
		SpeechSec: 30,
	}}, bench.ScoreOptions{WordsDir: filepath.Join(t.TempDir(), "missing"), RTTMDir: filepath.Join(t.TempDir(), "missing")})
	if err != nil {
		t.Fatal(err)
	}
	if len(metricsFile.Aggregate) != 0 {
		t.Fatalf("aggregate=%v", metricsFile.Aggregate)
	}
	if got := metricsFile.Denominators["meetings_unscored"]; got != float64(1) {
		t.Fatalf("meetings_unscored=%v", got)
	}
}

func TestMetricsFromSummaryKPIs_ImportsSyntheticAggregate(t *testing.T) {
	metricsFile, ok := bench.MetricsFromSummaryKPIs("run_summary_kpis", bench.ModalSummary{
		ProcessedAudioHours: 0.5,
		KPIs: map[string]bench.ModalKPI{
			"e2e.synthetic": {
				WERPct:            50.9,
				DERPct:            9.1,
				CpWERPct:          54.1,
				AttributionTaxPts: 3.2,
			},
		},
	})
	if !ok {
		t.Fatal("expected summary KPIs")
	}
	if got := metricsFile.Aggregate["wer"]; got != 0.509 {
		t.Fatalf("wer=%v", got)
	}
	if got := metricsFile.Aggregate["der"]; got != 0.091 {
		t.Fatalf("der=%v", got)
	}
	if got := metricsFile.Aggregate["cpwer"]; got != 0.541 {
		t.Fatalf("cpwer=%v", got)
	}
	if got := metricsFile.Aggregate["attribution_tax_pts"]; got != 3.2 {
		t.Fatalf("attribution_tax_pts=%v", got)
	}
	if got := metricsFile.Denominators["metrics_source"]; got != "modal_summary.kpis.e2e.synthetic" {
		t.Fatalf("metrics_source=%v", got)
	}
}

// A perfect synthetic run (0.0 across the board) must be RECORDED as 0.0, not
// dropped as "absent" — its reference denominators prove it was measured, and
// downstream gates treat a missing key as "no data" / fail.
func TestMetricsFromSummaryKPIs_RecordsMeasuredZero(t *testing.T) {
	metricsFile, ok := bench.MetricsFromSummaryKPIs("run_perfect", bench.ModalSummary{
		ProcessedAudioHours: 0.5,
		KPIs: map[string]bench.ModalKPI{
			"e2e.synthetic": {WERPct: 0, DERPct: 0, CpWERPct: 0, RefWords: 100, RefSpeechSec: 200},
		},
	})
	if !ok {
		t.Fatal("expected ok for a measured (perfect) run")
	}
	for _, k := range []string{"wer", "der", "cpwer"} {
		v, present := metricsFile.Aggregate[k]
		if !present {
			t.Fatalf("%s missing; a measured 0.0 must be recorded, not dropped", k)
		}
		if v != 0 {
			t.Fatalf("%s=%v; want 0.0", k, v)
		}
	}
}

// The STT-only source carries WER (RefWords) but no diarization reference, so
// DER/cpWER must NOT be invented — a false 0.0 would claim perfect diarization.
func TestMetricsFromSummaryKPIs_STTOnlyDoesNotInventDERCpWER(t *testing.T) {
	metricsFile, ok := bench.MetricsFromSummaryKPIs("run_stt", bench.ModalSummary{
		KPIs: map[string]bench.ModalKPI{
			"stt.synthetic.wer": {WERPct: 0, RefWords: 100},
		},
	})
	if !ok {
		t.Fatal("expected ok")
	}
	if _, present := metricsFile.Aggregate["wer"]; !present {
		t.Fatal("wer must be recorded (measured 0.0)")
	}
	if _, present := metricsFile.Aggregate["der"]; present {
		t.Fatal("der must be absent for an STT-only source (no diar reference)")
	}
	if _, present := metricsFile.Aggregate["cpwer"]; present {
		t.Fatal("cpwer must be absent for an STT-only source")
	}
}

// A meeting whose reference words are empty (but whose hypothesis emits words)
// must NOT fold its hypothesis insertions into the corpus WER numerator with a
// zero denominator — that would bias the aggregate upward without bound.
func TestScoreMeetingHyps_EmptyReferenceDoesNotInflateWER(t *testing.T) {
	root := t.TempDir()
	wordsDir := filepath.Join(root, "words")
	rttmDir := filepath.Join(root, "rttm")
	for _, d := range []string{wordsDir, rttmDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	// m_good: one reference word the hyp gets right → WER 0 over a denom of 1.
	write := func(name, body string, perm os.FileMode) {
		if err := os.WriteFile(filepath.Join(wordsDir, name+".words.json"), []byte(body), perm); err != nil {
			t.Fatal(err)
		}
	}
	write("m_good", `[{"speaker":"A","start":0.0,"end":0.4,"text":"hello"}]`, 0o600)
	// m_empty: a clean but EMPTY reference; the hyp hallucinates three words.
	write("m_empty", `[]`, 0o600)
	for _, id := range []string{"m_good", "m_empty"} {
		if err := os.WriteFile(filepath.Join(rttmDir, id+".rttm"),
			[]byte("SPEAKER "+id+" 1 0.000 1.000 <NA> <NA> A <NA> <NA>\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	metricsFile, err := bench.ScoreMeetingHyps("run_empty_ref", []bench.MeetingHyp{
		{MeetingID: "m_good", AudioSec: 60, SpeechSec: 30, Words: []bench.HypWord{{Text: "hello", Start: 0, End: 0.4, Speaker: "A"}}},
		{MeetingID: "m_empty", AudioSec: 60, SpeechSec: 30, Words: []bench.HypWord{
			{Text: "x", Start: 0, End: 0.2, Speaker: "A"},
			{Text: "y", Start: 0.3, End: 0.5, Speaker: "A"},
			{Text: "z", Start: 0.6, End: 0.8, Speaker: "A"},
		}},
	}, bench.ScoreOptions{WordsDir: wordsDir, RTTMDir: rttmDir})
	if err != nil {
		t.Fatal(err)
	}
	// Both meetings are scored, but the empty-reference one contributes nothing to
	// the WER aggregate, so the corpus WER is the good meeting's 0.0 — not 3.0.
	if got := metricsFile.Denominators["meetings_scored"]; got != float64(2) {
		t.Fatalf("meetings_scored=%v; want 2", got)
	}
	if got := metricsFile.Aggregate["wer"]; got != 0 {
		t.Fatalf("aggregate wer=%v; the empty-reference meeting's insertions must not inflate it (want 0)", got)
	}
}
