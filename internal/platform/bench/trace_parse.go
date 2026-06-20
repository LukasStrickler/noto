package bench

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
)

// PyannoteStageMap maps pyannote stderr hook names to canonical stage IDs.
type PyannoteStageMap map[string]string

// DefaultPyannoteStageMap is pyannote_stage_map.v1 from the plan.
var DefaultPyannoteStageMap = PyannoteStageMap{
	"segmentation": "diar_seg",
	"embeddings":   "diar_emb",
	"clustering":   "diar_cluster",
	"load_ms":      "model_load",
	"vad_ms":       "vad",
}

func LoadPyannoteStageMap(data []byte) (PyannoteStageMap, error) {
	m := PyannoteStageMap{}
	if len(data) == 0 {
		return DefaultPyannoteStageMap, nil
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// PyannoteTiming from stderr line.
type PyannoteTiming struct {
	StagesMS map[string]float64
	VADKept  float64
	LoadMS   float64
	InferMS  float64
}

var stagesMSRe = regexp.MustCompile(`stages_ms\s+([^\n]+)`)

// stagesBracketRe captures the bracketed per-stage group the LIVE pyannote server
// logs, e.g. `... kept=0.85 [segmentation=800 embeddings=4800 clustering=200]`.
// Requiring an `=` inside the brackets skips the `[pyannote-server]` log prefix
// (which has none). The server never writes the literal `stages_ms` token — that
// is only a dict key — so without this the live format parsed to nothing.
var stagesBracketRe = regexp.MustCompile(`\[([^\]\n]*=[^\]\n]*)\]`)
var stagePairRe = regexp.MustCompile(`(\w+)=([\d.]+)`)
var vadKeptRe = regexp.MustCompile(`kept=([\d.]+)`)

// ParsePyannoteStderr extracts stage timing from a pyannote server stderr line.
// It accepts BOTH the live bracketed format and the legacy `stages_ms <pairs>`
// form so a real run actually populates StagesMS instead of leaving it empty.
func ParsePyannoteStderr(line string) PyannoteTiming {
	t := PyannoteTiming{StagesMS: map[string]float64{}}
	stagesStr := ""
	if m := stagesBracketRe.FindStringSubmatch(line); len(m) == 2 {
		stagesStr = m[1]
	} else if m := stagesMSRe.FindStringSubmatch(line); len(m) == 2 {
		stagesStr = m[1]
	}
	for _, part := range strings.Fields(stagesStr) {
		if kv := stagePairRe.FindStringSubmatch(part); len(kv) == 3 {
			if v, err := strconv.ParseFloat(kv[2], 64); err == nil {
				t.StagesMS[kv[1]] = v
			}
		}
	}
	if m := vadKeptRe.FindStringSubmatch(line); len(m) == 2 {
		t.VADKept, _ = strconv.ParseFloat(m[1], 64)
	}
	return t
}

// MapStages converts hook names to canonical stage IDs.
func (m PyannoteStageMap) MapStages(hookMS map[string]float64) map[string]float64 {
	out := map[string]float64{}
	for hook, ms := range hookMS {
		id, ok := m[hook]
		if !ok {
			id = "unattributed"
		}
		out[id] += ms
	}
	return out
}

// MeetingHyp is per-meeting timing from AMI hyp JSON.
type MeetingHyp struct {
	ID          string    `json:"id,omitempty"`
	MeetingID   string    `json:"meeting_id"`
	STTMS       float64   `json:"stt_ms"`
	DiarMS      float64   `json:"diar_ms"`
	AudioSec    float64   `json:"audio_sec"`
	SpeechSec   float64   `json:"speech_sec"`
	NumSpeakers int       `json:"num_speakers,omitempty"`
	Words       []HypWord `json:"words,omitempty"`
	Turns       []HypTurn `json:"turns,omitempty"`
}

// HypWord is one attributed hypothesis word returned by the GPU capture phase.
// Confidence is the model's predicted P(correct) for the word when the STT
// engine emits one (NeMo on the parakeet-server path, §9.2 B2.6); it is a
// pointer so a run captured by an engine that emits no confidence is
// distinguishable from a genuine 0.0, and calibration is simply skipped for it.
type HypWord struct {
	Text       string   `json:"text"`
	Start      float64  `json:"start"`
	End        float64  `json:"end"`
	Speaker    string   `json:"speaker"`
	Confidence *float64 `json:"confidence,omitempty"`
}

// HypTurn is one diarization hypothesis turn returned by the GPU capture phase.
type HypTurn struct {
	Speaker string  `json:"speaker"`
	Start   float64 `json:"start"`
	End     float64 `json:"end"`
}

func (h MeetingHyp) Key() string {
	if h.MeetingID != "" {
		return h.MeetingID
	}
	return h.ID
}

// ModalSummary is the subset of modal_benchmark.py summary.json we ingest.
type ModalSummary struct {
	RunID               string              `json:"run_id"`
	EstimatedCostUSD    float64             `json:"estimated_cost_usd"`
	DurationMS          float64             `json:"duration_ms"`
	ProcessedAudioHours float64             `json:"processed_audio_hours"`
	CostPerAudioHourUSD float64             `json:"cost_per_audio_hour_usd"`
	GPU                 string              `json:"gpu"`
	CPUCores            float64             `json:"cpu_cores"`
	MemoryGiB           float64             `json:"memory_gib"`
	KPIs                map[string]ModalKPI `json:"kpis,omitempty"`

	// GPU sampling stats from nvidia-smi (modal_benchmark.py utilization_stats).
	GPUPeakMemoryMB       float64 `json:"gpu_peak_memory_mb,omitempty"`
	GPUPeakUtilizationPct float64 `json:"gpu_peak_utilization_pct,omitempty"`
	GPUMeanUtilizationPct float64 `json:"gpu_mean_utilization_pct,omitempty"`
	GPUBusyPct            float64 `json:"gpu_busy_pct,omitempty"`
	GPUHourlyUSD          float64 `json:"gpu_hourly_usd,omitempty"`
}

type ModalKPI struct {
	WERPct             float64 `json:"wer_pct,omitempty"`
	DERPct             float64 `json:"der_pct,omitempty"`
	CpWERPct           float64 `json:"cpwer_pct,omitempty"`
	AttributionTaxPts  float64 `json:"attribution_tax_pts,omitempty"`
	RefWords           int     `json:"ref_words,omitempty"`
	RefSpeechSec       float64 `json:"ref_speech_sec,omitempty"`
	MeanAttributionPct float64 `json:"mean_attribution_pct,omitempty"`
	RTF                float64 `json:"rtf,omitempty"`
}
