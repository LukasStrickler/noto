package bench

import "math"

// scale.go models how $/processed-audio-hour moves with run scale — the lever the
// $0.01/hr target lives or dies on. A run's cost splits into a per-RUN fixed
// overhead (startup: model load + CUDA init + warm-up, billed once no matter how
// much audio follows) and a per-audio-hour marginal rate (the STT+diar compute
// that scales). So:
//
//	cost(H)      = fixed + marginal·H
//	$/audio-hr(H) = fixed/H + marginal      (H = processed audio hours)
//
// $/hr falls toward `marginal` as H grows: the marginal rate is the TRUE floor
// scaling can reach. A target below it is unreachable by more hours alone and
// needs a utilization/batching/work-reduction win — which is exactly the
// distinction the cost program needs before it pours audio at the problem.

// SchemaScaleProjectionV1 identifies scale_projection.v1.
const SchemaScaleProjectionV1 = "scale_projection.v1"

// ScalePoint is one observed run: processed audio hours and the USD it cost.
type ScalePoint struct {
	AudioHours float64 `json:"audio_hours"`
	CostUSD    float64 `json:"cost_usd"`
}

// ScaleModel is the fitted decomposition. MarginalUSDPerAudioHour is the
// asymptotic $/hr floor; FixedUSD is the per-run startup overhead that amortizes.
type ScaleModel struct {
	FixedUSD                float64 `json:"fixed_usd"`
	MarginalUSDPerAudioHour float64 `json:"marginal_usd_per_audio_hour"`
}

// CostUSD is the modeled total cost to process `hours` of audio in one run.
func (m ScaleModel) CostUSD(hours float64) float64 {
	return roundUSD(m.FixedUSD + m.MarginalUSDPerAudioHour*hours)
}

// CostPerAudioHour is the modeled $/processed-audio-hour at scale `hours`. Returns
// the marginal floor for hours<=0 (the H→∞ limit), since fixed/H → 0 there.
func (m ScaleModel) CostPerAudioHour(hours float64) float64 {
	if hours <= 0 {
		return roundUSD(m.MarginalUSDPerAudioHour)
	}
	return roundUSD(m.FixedUSD/hours + m.MarginalUSDPerAudioHour)
}

// Floor is the lowest $/audio-hour this model can reach at any finite scale —
// the marginal rate. Scaling alone can approach but never beat it.
func (m ScaleModel) Floor() float64 { return roundUSD(m.MarginalUSDPerAudioHour) }

// HoursToReach is the audio-hour scale at which $/audio-hr first drops to
// `target`. ok is false when the target is at or below the marginal floor
// (unreachable by scale — needs a rate win), in which case hours is 0.
func (m ScaleModel) HoursToReach(target float64) (hours float64, ok bool) {
	gap := target - m.MarginalUSDPerAudioHour
	if gap <= 0 {
		return 0, false // target ≤ floor: more hours can't get there
	}
	if m.FixedUSD <= 0 {
		return 0, true // no fixed overhead: already at the floor for any H>0
	}
	return m.FixedUSD / gap, true
}

// FitScaleModel fits cost = fixed + marginal·hours by least squares over the
// observed runs. It needs ≥2 points spanning ≥2 distinct audio-hour values
// (otherwise fixed and marginal can't be separated). A non-positive fitted slope
// is non-physical (more audio cannot cost less on the margin) — it means the data
// is too flat/noisy to measure the marginal rate, so the fit is REJECTED rather
// than reported as a marginal-0 model (which would assert a false $0 floor and
// "reachable by scale"); the caller falls back to the idle-based single-run floor.
// A tiny negative intercept from a slightly super-linear pair is clamped to 0.
func FitScaleModel(points []ScalePoint) (ScaleModel, bool) {
	if len(points) < 2 {
		return ScaleModel{}, false
	}
	var n, sumH, sumC, sumHH, sumHC float64
	first := points[0].AudioHours
	distinct := false
	for _, p := range points {
		if p.AudioHours != first {
			distinct = true
		}
		n++
		sumH += p.AudioHours
		sumC += p.CostUSD
		sumHH += p.AudioHours * p.AudioHours
		sumHC += p.AudioHours * p.CostUSD
	}
	if !distinct {
		return ScaleModel{}, false // all runs same scale: slope undetermined
	}
	denom := n*sumHH - sumH*sumH
	if denom == 0 {
		return ScaleModel{}, false
	}
	marginal := (n*sumHC - sumH*sumC) / denom
	if marginal <= 0 {
		// Non-physical slope: reject so the caller falls back to the idle-based
		// floor, rather than asserting a marginal-0 model (false $0 floor). Must be
		// checked BEFORE deriving fixed — a fixed computed from the discarded slope
		// would be inconsistent with marginal=0.
		return ScaleModel{}, false
	}
	fixed := (sumC - marginal*sumH) / n
	if fixed < 0 {
		fixed = 0 // a slightly super-linear pair can yield a tiny negative intercept
	}
	return ScaleModel{FixedUSD: roundUSD(fixed), MarginalUSDPerAudioHour: roundUSD(marginal)}, true
}

// ScaleModelFromIdle decomposes a SINGLE run using its measured GPU idle cost as
// the fixed-overhead proxy: idle GPU dollars (billed while the card was below the
// busy threshold) are dominated by the audio-independent startup, and the busy
// dollars are the marginal compute. It is a conservative UPPER bound on the floor
// — idle also carries a small per-meeting scheduling component, so the true
// marginal is no higher than this — and is the fallback when only one run exists.
func ScaleModelFromIdle(totalUSD, idleUSD, audioHours float64) (ScaleModel, bool) {
	if audioHours <= 0 || totalUSD <= 0 {
		return ScaleModel{}, false
	}
	if idleUSD < 0 {
		idleUSD = 0
	}
	if idleUSD > totalUSD {
		idleUSD = totalUSD
	}
	marginal := (totalUSD - idleUSD) / audioHours
	return ScaleModel{FixedUSD: roundUSD(idleUSD), MarginalUSDPerAudioHour: roundUSD(marginal)}, true
}

// ScaleReadinessThresholds gates escalating a benchmark from a cheap subsample to
// the full anchor: the "scale only when subsample performance is good AND
// utilization on ~10h of meeting is good" rule. Spending GPU on 30 meetings is
// only justified once a subsample shows the quality holds, the card stays busy,
// and the modeled cost at the 10h reference scale is still on track for the
// target — otherwise the scale-up just multiplies a known-bad run.
type ScaleReadinessThresholds struct {
	MinBusyPct                  float64 // GPU must stay at least this busy on the subsample
	MaxCostPerAudioHourAt10hUSD float64 // modeled $/audio-hr at the reference scale must be ≤ this
	ReferenceScaleHours         float64 // the "10h of meeting" the projection is read at
}

// DefaultScaleReadinessThresholds returns the gate thresholds: the L40S anchor
// held ~82% busy, so 70% is a generous floor below which the card is mostly idle;
// the 10h cost must not exceed the audited anchor rate (scaling should not make
// $/audio-hr worse than the known-good baseline); read at 10h of audio.
func DefaultScaleReadinessThresholds() ScaleReadinessThresholds {
	return ScaleReadinessThresholds{
		MinBusyPct:                  70,
		MaxCostPerAudioHourAt10hUSD: AnchorCostPerProcessedAudioHourUSD,
		ReferenceScaleHours:         10,
	}
}

// ScaleReadiness is the gate decision: may the harness scale this config to the
// full anchor? Ready is true only when every condition passes; Reasons lists the
// ones that failed (empty when ready).
type ScaleReadiness struct {
	SchemaVersion            string   `json:"schema_version"`
	Ready                    bool     `json:"ready"`
	BusyPct                  float64  `json:"busy_pct"`
	MinBusyPct               float64  `json:"min_busy_pct"`
	QualityWithinGuardrails  bool     `json:"quality_within_guardrails"`
	CostPerAudioHourAt10hUSD float64  `json:"cost_per_audio_hour_at_10h_usd"`
	ReferenceScaleHours      float64  `json:"reference_scale_hours"`
	Reasons                  []string `json:"reasons,omitempty"`
}

// SchemaScaleReadinessV1 identifies scale_readiness.v1.
const SchemaScaleReadinessV1 = "scale_readiness.v1"

// EvaluateScaleReadiness decides whether a subsample has earned a full-anchor run.
// busyPct + qualityOK come from the subsample; model is its cost decomposition (so
// the 10h projection reflects real fixed/marginal behavior). All three gates must
// pass: quality within guardrails, GPU busy enough, and modeled 10h cost on track.
func EvaluateScaleReadiness(busyPct float64, qualityOK bool, model ScaleModel, th ScaleReadinessThresholds) ScaleReadiness {
	if th.ReferenceScaleHours <= 0 {
		th.ReferenceScaleHours = 10
	}
	costAt10h := model.CostPerAudioHour(th.ReferenceScaleHours)
	r := ScaleReadiness{
		SchemaVersion:            SchemaScaleReadinessV1,
		BusyPct:                  roundUSD(busyPct),
		MinBusyPct:               th.MinBusyPct,
		QualityWithinGuardrails:  qualityOK,
		CostPerAudioHourAt10hUSD: costAt10h,
		ReferenceScaleHours:      th.ReferenceScaleHours,
	}
	if !qualityOK {
		r.Reasons = append(r.Reasons, "subsample quality outside guardrails — fix accuracy before scaling")
	}
	if th.MinBusyPct > 0 && busyPct < th.MinBusyPct {
		r.Reasons = append(r.Reasons, "gpu utilization below floor — scaling would rent an idle card")
	}
	if th.MaxCostPerAudioHourAt10hUSD > 0 && costAt10h > th.MaxCostPerAudioHourAt10hUSD {
		r.Reasons = append(r.Reasons, "projected $/audio-hr at 10h exceeds the ceiling — cost not on track at scale")
	}
	r.Ready = len(r.Reasons) == 0
	return r
}

// ScaleProjectionPoint is the modeled cost at one scale.
type ScaleProjectionPoint struct {
	AudioHours          float64 `json:"audio_hours"`
	CostUSD             float64 `json:"cost_usd"`
	CostPerAudioHourUSD float64 `json:"cost_per_audio_hour_usd"`
}

// ScaleProjection is scale_projection.v1 — the $/audio-hour curve plus whether
// the target is reachable by scale and at what audio-hour scale it crosses.
type ScaleProjection struct {
	SchemaVersion           string                 `json:"schema_version"`
	FixedUSD                float64                `json:"fixed_usd"`
	MarginalUSDPerAudioHour float64                `json:"marginal_usd_per_audio_hour"`
	FloorUSDPerAudioHour    float64                `json:"floor_usd_per_audio_hour"`
	TargetUSDPerAudioHour   float64                `json:"target_usd_per_audio_hour"`
	TargetReachableByScale  bool                   `json:"target_reachable_by_scale"`
	HoursToReachTarget      float64                `json:"hours_to_reach_target,omitempty"`
	Points                  []ScaleProjectionPoint `json:"points"`
	Readiness               *ScaleReadiness        `json:"readiness,omitempty"`
	Notes                   []string               `json:"notes,omitempty"`
}

// ProjectScale renders the cost curve at the given scales and answers the two
// questions the $0.01 target raises: is it reachable by adding audio at all
// (target > floor), and if so at what scale. scales is the set of audio-hour
// points to report (e.g. 1, 10, 100); a nil/empty set defaults to 1/10/100.
func ProjectScale(m ScaleModel, target float64, scales []float64) ScaleProjection {
	if len(scales) == 0 {
		scales = []float64{1, 10, 100}
	}
	p := ScaleProjection{
		SchemaVersion:           SchemaScaleProjectionV1,
		FixedUSD:                roundUSD(m.FixedUSD),
		MarginalUSDPerAudioHour: roundUSD(m.MarginalUSDPerAudioHour),
		FloorUSDPerAudioHour:    m.Floor(),
		TargetUSDPerAudioHour:   roundUSD(target),
	}
	for _, h := range scales {
		p.Points = append(p.Points, ScaleProjectionPoint{
			AudioHours:          h,
			CostUSD:             m.CostUSD(h),
			CostPerAudioHourUSD: m.CostPerAudioHour(h),
		})
	}
	hours, ok := m.HoursToReach(target)
	p.TargetReachableByScale = ok
	if ok {
		p.HoursToReachTarget = math.Round(hours*100) / 100
		if m.FixedUSD <= 0 {
			p.Notes = append(p.Notes, "no measurable fixed overhead: already at the marginal floor for any scale")
		}
	} else {
		p.Notes = append(p.Notes, "target at or below the marginal floor: unreachable by scaling audio alone — needs a utilization / batching / work-reduction win")
	}
	return p
}
