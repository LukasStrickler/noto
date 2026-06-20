package bench

import "fmt"

// kpi.go is the WEIGHTED composite score the bench program develops against: one
// 0..100 number that folds the three dials that matter — $/processed-audio-hour
// (the north star), quality margin under the §4.1 guardrails, and GPU efficiency
// (busy% / idle waste) — plus a transparent per-component breakdown so a developer
// sees WHY a run scored what it did. It is pure (no I/O); the service feeds it the
// values it has already loaded.

// Quality guardrail thresholds (§4.1 anchor), as the FRACTIONS metrics.json stores
// (wer/der/cpwer). A run at or below each holds quality.
const (
	GuardrailWER   = 0.235
	GuardrailDER   = 0.113
	GuardrailCpWER = 0.311
)

// KPIWeights weights the composite across its three dials. They need not sum to 1
// — ScoreRun normalizes by their sum. The default leans on cost (the program's
// north star) and GPU efficiency (idle is wasted money), with quality as a margin
// reward on top of its hard gate.
type KPIWeights struct {
	Cost        float64 `json:"cost"`
	Quality     float64 `json:"quality"`
	Utilization float64 `json:"utilization"`
}

// DefaultKPIWeights — cost-led, utilization a strong second (idle = money), quality
// a margin reward (it is ALSO a hard gate, below).
func DefaultKPIWeights() KPIWeights { return KPIWeights{Cost: 0.5, Quality: 0.2, Utilization: 0.3} }

// KPIInputs are the measured values a run contributes to its weighted score. A
// quality metric <= 0 is treated as ABSENT (integration/smoke runs) and skipped.
type KPIInputs struct {
	CostPerAudioHourUSD     float64
	AnchorCostUSD           float64 // worst-acceptable cost — maps to 0 (§3.1)
	TargetCostUSD           float64 // ideal cost — maps to 1 (§1 stretch)
	WER                     float64
	DER                     float64
	CpWER                   float64
	BusyPct                 float64
	IdleCostPerAudioHourUSD float64
}

// WeightedScore is a 0..100 composite plus its transparent component breakdown.
// A quality-guardrail breach is a HARD GATE (§4.1): it zeroes the score regardless
// of cost/utilization, because a cost win that regresses quality is a reject, not a
// trade-off. The weighted quality term only rewards margin among passing runs.
type WeightedScore struct {
	Score          float64            `json:"score"`
	GuardrailsPass bool               `json:"guardrails_pass"`
	Components     map[string]float64 `json:"components"` // cost/quality/utilization, each 0..1
	Notes          []string           `json:"notes,omitempty"`
}

// ScoreRun computes the weighted composite. costScore maps anchor→0, target→1
// (clamped, so beating the target still caps at 1); qualityScore is the mean margin
// under each PRESENT guardrail; utilScore is the GPU busy fraction. The weighted
// mean is gated to 0 on any guardrail breach.
func ScoreRun(in KPIInputs, w KPIWeights) WeightedScore {
	comp := map[string]float64{}
	var notes []string
	// num/wsum accumulate ONLY the dials a run was actually measured on. An absent
	// dial is dropped from BOTH — scoring it as the worst value (0) while leaving
	// its weight in the denominator would penalize a run for not being measured,
	// so two runs identical on the dials they share would score differently purely
	// because one carried an extra (absent) metric. The gate already excludes
	// absent quality; the weighted mean now excludes absent dials the same way.
	var num, wsum float64

	// Cost: present when measured (>0) and the anchor/target band is valid. cost==0
	// here means "no cost audit" (store sets 0 when the audit is missing), not free.
	costScore := 0.0
	if in.CostPerAudioHourUSD > 0 && in.AnchorCostUSD > in.TargetCostUSD {
		costScore = clamp01((in.AnchorCostUSD - in.CostPerAudioHourUSD) / (in.AnchorCostUSD - in.TargetCostUSD))
		num += w.Cost * costScore
		wsum += w.Cost
	} else {
		notes = append(notes, "no cost metric on this run — cost component scored 0 (not in the weighted mean)")
	}
	comp["cost"] = costScore

	var qSum float64
	var qN int
	pass := true
	addQ := func(val, thr float64) {
		if val <= 0 {
			return // absent
		}
		qSum += clamp01((thr - val) / thr)
		qN++
		if val > thr {
			pass = false
		}
	}
	addQ(in.WER, GuardrailWER)
	addQ(in.DER, GuardrailDER)
	addQ(in.CpWER, GuardrailCpWER)
	qualityScore := 0.0
	if qN > 0 {
		qualityScore = qSum / float64(qN)
		num += w.Quality * qualityScore
		wsum += w.Quality
	} else {
		notes = append(notes, "no quality metrics on this run — quality component scored 0 (not gated, not in the weighted mean)")
	}
	comp["quality"] = qualityScore

	// Utilization is always a real reading: a GPU run reports busy%, a CPU run is a
	// genuine 0%. So it always contributes (unlike cost/quality, which can be absent).
	utilScore := clamp01(in.BusyPct / 100)
	comp["utilization"] = utilScore
	num += w.Utilization * utilScore
	wsum += w.Utilization

	if wsum <= 0 {
		wsum = 1
	}
	score := 100 * num / wsum

	if !pass {
		score = 0
		notes = append(notes, "quality guardrail breached — score gated to 0 (a cost win that regresses quality is a reject, §4.1)")
	}
	if in.IdleCostPerAudioHourUSD > 0 {
		notes = append(notes, fmt.Sprintf("GPU idle: $%.4f/audio-hr billed while the card was not busy — raise utilization to recover it", in.IdleCostPerAudioHourUSD))
	}
	return WeightedScore{Score: score, GuardrailsPass: pass, Components: comp, Notes: notes}
}
