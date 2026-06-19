package bench

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/lukasstrickler/noto/benchmark/dataset"
	"github.com/lukasstrickler/noto/benchmark/metrics"
	corebench "github.com/lukasstrickler/noto/internal/core/bench"
)

// ScoreOptions points the local scorer at reference artifacts.
type ScoreOptions struct {
	WordsDir string
	RTTMDir  string
}

func ScoreOptionsForRepo(repoRoot string, suite SuiteSpec) ScoreOptions {
	if suite.ModalSuite == "synthetic" {
		dir := filepath.Join(repoRoot, "benchmark", "dataset", "synthetic_meetings")
		return ScoreOptions{WordsDir: dir, RTTMDir: dir}
	}
	return ScoreOptions{
		WordsDir: filepath.Join(repoRoot, "benchmark", "dataset", "words"),
		RTTMDir:  filepath.Join(repoRoot, "benchmark", "identity", "ami"),
	}
}

// ScoreMeetingHyps computes local quality metrics from returned hypothesis JSON.
func ScoreMeetingHyps(runID string, hyps []MeetingHyp, opts ScoreOptions) (corebench.MetricsFile, error) {
	out := corebench.MetricsFile{
		SchemaVersion: corebench.SchemaMetricsV1,
		RunID:         runID,
		Aggregate:     map[string]float64{},
		Denominators:  map[string]any{},
		Slices:        map[string]any{},
	}

	var sums qualitySums
	var unscored []string
	for _, hyp := range hyps {
		id := hyp.Key()
		if id == "" {
			unscored = append(unscored, "missing_meeting_id")
			continue
		}
		addDuration(&sums, hyp)
		row, err := scoreMeetingHyp(id, hyp, opts)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				unscored = append(unscored, id)
				continue
			}
			return out, err
		}
		sums.add(row)
	}

	if sums.MeetingsScored > 0 {
		out.Aggregate["wer"] = divide(sums.WERErrors, sums.WERRef)
		out.Aggregate["der"] = divide(sums.DERErrors, sums.DERRef)
		out.Aggregate["cpwer"] = divide(sums.CpWERErrors, sums.CpWERRef)
	}
	out.Denominators["meetings_scored"] = float64(sums.MeetingsScored)
	out.Denominators["meetings_unscored"] = float64(len(unscored))
	out.Denominators["ref_words"] = sums.WERRef
	out.Denominators["ref_speech_sec"] = sums.DERRef
	out.Denominators["audio_hours"] = sums.AudioSec / 3600
	out.Denominators["speech_hours"] = sums.SpeechSec / 3600
	if len(unscored) > 0 {
		sort.Strings(unscored)
		out.Slices["unscored_meetings"] = unscored
	}
	return out, nil
}

func MetricsFromSummaryKPIs(runID string, summary ModalSummary) (corebench.MetricsFile, bool) {
	out := corebench.MetricsFile{
		SchemaVersion: corebench.SchemaMetricsV1,
		RunID:         runID,
		Aggregate:     map[string]float64{},
		Denominators:  map[string]any{},
		Slices:        map[string]any{},
	}
	kpi, ok := summary.KPIs["e2e.synthetic"]
	source := "modal_summary.kpis.e2e.synthetic"
	if !ok {
		kpi, ok = summary.KPIs["stt.synthetic.wer"]
		source = "modal_summary.kpis.stt.synthetic.wer"
	}
	if !ok {
		return out, false
	}
	if kpi.WERPct != 0 {
		out.Aggregate["wer"] = kpi.WERPct / 100
	}
	if kpi.DERPct != 0 {
		out.Aggregate["der"] = kpi.DERPct / 100
	}
	if kpi.CpWERPct != 0 {
		out.Aggregate["cpwer"] = kpi.CpWERPct / 100
	}
	if kpi.AttributionTaxPts != 0 {
		out.Aggregate["attribution_tax_pts"] = kpi.AttributionTaxPts
	}
	if kpi.RTF != 0 {
		out.Aggregate["rtf"] = kpi.RTF
	}
	out.Denominators["metrics_source"] = source
	out.Denominators["audio_hours"] = summary.ProcessedAudioHours
	if kpi.RefWords > 0 {
		out.Denominators["ref_words"] = float64(kpi.RefWords)
	}
	if kpi.RefSpeechSec > 0 {
		out.Denominators["ref_speech_sec"] = kpi.RefSpeechSec
	}
	return out, len(out.Aggregate) > 0
}

type qualitySums struct {
	MeetingsScored int
	WERErrors      float64
	WERRef         float64
	DERErrors      float64
	DERRef         float64
	CpWERErrors    float64
	CpWERRef       float64
	AudioSec       float64
	SpeechSec      float64
}

type qualityRow struct {
	WERErrors   float64
	WERRef      float64
	DERErrors   float64
	DERRef      float64
	CpWERErrors float64
	CpWERRef    float64
}

func (s *qualitySums) add(row qualityRow) {
	s.MeetingsScored++
	s.WERErrors += row.WERErrors
	s.WERRef += row.WERRef
	s.DERErrors += row.DERErrors
	s.DERRef += row.DERRef
	s.CpWERErrors += row.CpWERErrors
	s.CpWERRef += row.CpWERRef
}

func addDuration(s *qualitySums, hyp MeetingHyp) {
	s.AudioSec += hyp.AudioSec
	s.SpeechSec += hyp.SpeechSec
}

func scoreMeetingHyp(id string, hyp MeetingHyp, opts ScoreOptions) (qualityRow, error) {
	refWords, err := dataset.LoadWords(filepath.Join(opts.WordsDir, id+".words.json"))
	if err != nil {
		return qualityRow{}, err
	}
	refTurns, err := dataset.LoadRTTM(filepath.Join(opts.RTTMDir, id+".rttm"))
	if err != nil {
		return qualityRow{}, err
	}
	ref := dataset.Meeting{ID: id, Words: refWords, Turns: refTurns}

	wer := metrics.WER(ref.Reference(), hypWordTokens(hyp.Words))
	der := metrics.DER(ref.Segments(), hypTurnsToSegments(hyp.Turns), metrics.DefaultDEROptions())
	cpwer := metrics.CpWER(ref.ReferenceBySpeaker(), hypWordsBySpeaker(hyp.Words))

	return qualityRow{
		WERErrors:   float64(wer.Errors()),
		WERRef:      float64(wer.RefLen),
		DERErrors:   der.Total(),
		DERRef:      der.RefSpeech,
		CpWERErrors: float64(cpwer.Errors()),
		CpWERRef:    float64(cpwer.RefLen),
	}, nil
}

func hypWordTokens(words []HypWord) []string {
	ws := append([]HypWord(nil), words...)
	sort.SliceStable(ws, func(i, j int) bool { return ws[i].Start < ws[j].Start })
	parts := make([]string, len(ws))
	for i, w := range ws {
		parts[i] = w.Text
	}
	return metrics.Normalize(strings.Join(parts, " "))
}

func hypWordsBySpeaker(words []HypWord) map[string][]string {
	bySpeaker := map[string][]HypWord{}
	for _, w := range words {
		bySpeaker[w.Speaker] = append(bySpeaker[w.Speaker], w)
	}
	out := map[string][]string{}
	for speaker, ws := range bySpeaker {
		sort.SliceStable(ws, func(i, j int) bool { return ws[i].Start < ws[j].Start })
		parts := make([]string, len(ws))
		for i, w := range ws {
			parts[i] = w.Text
		}
		toks := metrics.Normalize(strings.Join(parts, " "))
		if len(toks) > 0 {
			out[speaker] = toks
		}
	}
	return out
}

func hypTurnsToSegments(turns []HypTurn) []metrics.Segment {
	out := make([]metrics.Segment, len(turns))
	for i, turn := range turns {
		out[i] = metrics.Segment{Speaker: turn.Speaker, Start: turn.Start, End: turn.End}
	}
	return out
}

func divide(num, denom float64) float64 {
	if denom == 0 {
		return 0
	}
	return num / denom
}
