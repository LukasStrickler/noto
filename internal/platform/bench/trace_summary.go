package bench

import (
	"math"
	"strings"

	corebench "github.com/lukasstrickler/noto/internal/core/bench"
)

const AllocationParallelMaxV1 = "parallel_max_v1"

// BuildTraceSummary constructs trace_summary.v1 from modal summary + meeting hyps + pyannote timing.
func BuildTraceSummary(summary ModalSummary, hyps []MeetingHyp, pyannote PyannoteTiming, stageMap PyannoteStageMap) corebench.TraceSummary {
	total := summary.EstimatedCostUSD
	perMeetings := allocateMeetings(total, hyps, pyannote, stageMap)
	computeStages := aggregateStages(perMeetings)
	traceMode := "summary"
	if len(hyps) == 0 && total > 0 {
		traceMode = "summary_coarse"
		computeStages = []corebench.StageCost{{
			Stage:        "e2e_pipeline",
			System:       "benchmark_harness",
			WallMS:       summary.DurationMS,
			USD:          round4(total),
			PctOfCompute: 100,
		}}
	}

	computeUSD := 0.0
	for _, s := range computeStages {
		computeUSD += s.USD
	}
	unattributed := math.Max(0, total-computeUSD)
	unattributedPct := 0.0
	if total > 0 {
		unattributedPct = unattributed / total * 100
	}

	return corebench.TraceSummary{
		SchemaVersion: corebench.SchemaTraceSummaryV1,
		RunID:         summary.RunID,
		TraceMode:     traceMode,
		CostWaterfall: corebench.CostWaterfall{
			TotalUSD:        round4(total),
			ComputeUSD:      round4(computeUSD),
			UnattributedUSD: round4(unattributed),
			UnattributedPct: round4(unattributedPct),
		},
		ComputeByStage: computeStages,
		PerMeeting:     perMeetings,
		GPU:            gpuUtilization(summary),
	}
}

// gpuUtilization carries nvidia-smi sampling stats into the trace and derives
// the idle-cost KPI (GPU dollars billed while the card was below the busy
// threshold). Returns nil for runs with no GPU samples (e.g. CPU smoke runs).
func gpuUtilization(s ModalSummary) *corebench.GPUUtilization {
	if s.GPUMeanUtilizationPct == 0 && s.GPUBusyPct == 0 && s.GPUPeakUtilizationPct == 0 && s.GPUPeakMemoryMB == 0 {
		return nil
	}
	g := &corebench.GPUUtilization{
		MeanUtilizationPct: s.GPUMeanUtilizationPct,
		PeakUtilizationPct: s.GPUPeakUtilizationPct,
		BusyPct:            s.GPUBusyPct,
		PeakVRAMMB:         s.GPUPeakMemoryMB,
		HourlyUSD:          s.GPUHourlyUSD,
	}
	// Idle cost needs the GPU rate, the wall clock, and real samples (mean>0
	// proves nvidia-smi reported, so a 0% busy reading is genuine idle, not a
	// missing probe).
	if s.GPUHourlyUSD > 0 && s.DurationMS > 0 && s.GPUMeanUtilizationPct > 0 {
		gpuCost := s.GPUHourlyUSD * s.DurationMS / 3600000
		idle := gpuCost * (1 - s.GPUBusyPct/100)
		if idle < 0 {
			idle = 0
		}
		g.GPUCostUSD = round4(gpuCost)
		g.IdleCostUSD = round4(idle)
		if s.ProcessedAudioHours > 0 {
			g.IdleCostPerAudioHourUSD = round4(idle / s.ProcessedAudioHours)
		}
	}
	return g
}

func allocateMeetings(containerUSD float64, hyps []MeetingHyp, pyannote PyannoteTiming, stageMap PyannoteStageMap) []corebench.PerMeetingTrace {
	if len(hyps) == 0 {
		return nil
	}
	maxWalls := make([]float64, len(hyps))
	sumMax := 0.0
	for i, h := range hyps {
		mw := math.Max(h.STTMS, h.DiarMS)
		maxWalls[i] = mw
		sumMax += mw
	}
	diarSub := 0.0
	mapped := stageMap.MapStages(pyannote.StagesMS)
	for stage, ms := range mapped {
		if stage == "diar_seg" || stage == "diar_emb" || stage == "diar_cluster" {
			diarSub += ms
		}
	}
	if diarSub <= 0 {
		diarSub = mapped["diar_emb"]
	}

	out := make([]corebench.PerMeetingTrace, len(hyps))
	for i, h := range hyps {
		share := 1.0
		if sumMax > 0 {
			share = maxWalls[i] / sumMax
		}
		meetingUSD := containerUSD * share
		sttMS := h.STTMS
		// Split the meeting cost across asr vs diar by their work. Prefer the
		// pyannote stderr substage timing (diarSub, precise seg+emb+cluster);
		// when it is unavailable — live runs where the server's stages_ms stderr
		// isn't captured — fall back to the per-meeting diar wall already carried
		// in the hyp, so diar (the §5.6 GPU bottleneck) gets its real share
		// instead of collapsing to $0 with everything billed to asr.
		diarWork := diarSub
		if diarWork <= 0 {
			diarWork = h.DiarMS
		}
		denom := sttMS + diarWork
		asrUSD := meetingUSD
		diarUSD := 0.0
		if denom > 0 {
			asrUSD = meetingUSD * (sttMS / denom)
			diarUSD = meetingUSD - asrUSD
		}
		usdByStage := map[string]float64{
			"asr":      round4(asrUSD),
			"diar_emb": round4(diarUSD),
		}
		speech := h.SpeechSec
		if speech <= 0 && pyannote.VADKept > 0 {
			speech = h.AudioSec * pyannote.VADKept
		}
		diarEmbMS := mapped["diar_emb"]
		if diarEmbMS <= 0 {
			diarEmbMS = diarWork
		}
		out[i] = corebench.PerMeetingTrace{
			MeetingID:        h.MeetingID,
			AudioSec:         h.AudioSec,
			SpeechSec:        speech,
			WallMS:           maxWalls[i],
			USD:              round4(meetingUSD),
			OverlapMS:        math.Max(0, sttMS+h.DiarMS-maxWalls[i]),
			AllocationMethod: AllocationParallelMaxV1,
			USDByStage:       usdByStage,
			Stages: map[string]float64{
				"asr_ms":      sttMS,
				"diar_emb_ms": diarEmbMS,
			},
		}
	}
	return out
}

func aggregateStages(meetings []corebench.PerMeetingTrace) []corebench.StageCost {
	totals := map[string]float64{}
	for _, m := range meetings {
		for stage, usd := range m.USDByStage {
			totals[stage] += usd
		}
	}
	computeUSD := 0.0
	for _, v := range totals {
		computeUSD += v
	}
	var stages []corebench.StageCost
	for stage, usd := range totals {
		pct := 0.0
		if computeUSD > 0 {
			pct = usd / computeUSD * 100
		}
		system := "stt_server"
		if strings.HasPrefix(stage, "diar") {
			system = "diar_server"
		}
		stages = append(stages, corebench.StageCost{
			Stage: stage, System: system, USD: round4(usd), PctOfCompute: round4(pct),
		})
	}
	return stages
}

func round4(v float64) float64 {
	return math.Round(v*10000) / 10000
}
