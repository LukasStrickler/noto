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
