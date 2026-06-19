package service

import (
	"context"
	"os"
	"os/exec"
	"strings"

	corebench "github.com/lukasstrickler/noto/internal/core/bench"
	platformbench "github.com/lukasstrickler/noto/internal/platform/bench"
	"github.com/lukasstrickler/noto/internal/transport/notoapi"
)

const schemaBenchRunV1 = "bench_run.v1"

func (s *Service) benchRunner() *platformbench.Runner {
	cfg := s.currentCfg()
	runner := platformbench.NewRunner(platformbench.NewStore(cfg.ConfigDir))
	if root, err := platformbench.RepoRoot(); err == nil {
		runner.Modal = platformbench.NewModalRunner(runner.Store, root)
	}
	return runner
}

func (s *Service) buildRunManifest(req notoapi.BenchRunRequest) corebench.RunManifest {
	tier := req.Tier
	if tier == "" {
		tier = "gate"
	}
	mode := req.OperatingMode
	if mode == "" {
		mode = "batch_queue"
	}
	profile := req.ExecutionProfile
	if profile == "" {
		profile = "modal_cuda"
	}
	budget := platformbench.EffectiveBudget(tier, req.BudgetUSDCap)
	return corebench.RunManifest{
		SchemaVersion:    corebench.SchemaManifestV1,
		SuiteID:          req.SuiteID,
		Tier:             tier,
		ExecutionProfile: profile,
		OperatingMode:    mode,
		BudgetUSDCap:     budget,
		Knobs:            req.Knobs,
		HypothesisID:     req.HypothesisID,
		ProgramStep:      req.ProgramStep,
		IntegrationOnly:  req.IntegrationOnly,
		AgentID:          req.AgentID,
		BenchStack:       "bench_cuda",
		GitCommit:        gitHead(),
		Comparability: corebench.ManifestCompat{
			TargetConcurrency: platformbench.TargetConcurrency(mode, req.Knobs),
			CacheState:        "warm_models_cold_audio",
			TraceMode:         "summary",
		},
	}
}

func gitHead() string {
	out, err := exec.Command("git", "rev-parse", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func (s *Service) BenchEstimate(ctx context.Context, req notoapi.BenchEstimateRequest) (notoapi.BenchEstimateResult, error) {
	_ = ctx
	if strings.TrimSpace(req.SuiteID) == "" {
		return notoapi.BenchEstimateResult{}, notoapi.NewError(notoapi.CodeInvalidRequest, "suite_id required", nil)
	}
	tier := req.Tier
	if tier == "" {
		tier = "gate"
	}
	est := corebench.Estimate(corebench.EstimateInput{
		SuiteID:    req.SuiteID,
		Tier:       tier,
		AudioHours: platformbench.SuiteAudioHours(req.SuiteID),
		TierCapUSD: platformbench.EffectiveBudget(tier, 0),
	})
	return notoapi.BenchEstimateResult{
		SchemaVersion:                      est.SchemaVersion,
		SuiteID:                            est.SuiteID,
		Tier:                               est.Tier,
		AudioHours:                         est.AudioHours,
		AnchorCostPerProcessedAudioHourUSD: est.AnchorCostPerProcessedAudioHourUSD,
		ProjectedCostUSD:                   est.ProjectedCostUSD,
		TierBudgetCapUSD:                   est.TierBudgetCapUSD,
		WithinBudget:                       est.WithinBudget,
		TargetCostPerProcessedAudioHourUSD: est.TargetCostPerProcessedAudioHourUSD,
		IdleHeadroomPerAudioHourUSD:        est.IdleHeadroomPerAudioHourUSD,
		ProjectedTargetCostUSD:             est.ProjectedTargetCostUSD,
		ProjectedHeadroomSavingsUSD:        est.ProjectedHeadroomSavingsUSD,
		Notes:                              est.Notes,
	}, nil
}

func (s *Service) BenchPreflight(ctx context.Context, req notoapi.BenchPreflightRequest) (notoapi.BenchPreflightResult, error) {
	manifest := corebench.RunManifest{
		SchemaVersion:    corebench.SchemaManifestV1,
		SuiteID:          req.SuiteID,
		Tier:             req.Tier,
		ExecutionProfile: req.ExecutionProfile,
		OperatingMode:    req.OperatingMode,
		BudgetUSDCap:     platformbench.EffectiveBudget(req.Tier, req.BudgetUSDCap),
		Knobs:            req.Knobs,
		IntegrationOnly:  req.IntegrationOnly,
		AgentID:          req.AgentID,
	}
	resp, err := s.benchRunner().Preflight(ctx, platformbench.PreflightRequest{Manifest: manifest})
	if err != nil {
		return notoapi.BenchPreflightResult{}, notoapi.NewError(notoapi.CodeInternal, err.Error(), nil)
	}
	return notoapi.BenchPreflightResult{
		SchemaVersion:      resp.SchemaVersion,
		SuiteID:            resp.SuiteID,
		Tier:               resp.Tier,
		BudgetUSDCap:       resp.BudgetUSDCap,
		ProjectedCostUSD:   resp.ProjectedCostUSD,
		ComparableToWinner: resp.ComparableToWinner,
		Blockers:           resp.Blockers,
		Warnings:           resp.Warnings,
	}, nil
}

func (s *Service) BenchRun(ctx context.Context, req notoapi.BenchRunRequest) (notoapi.BenchRunResult, error) {
	if strings.TrimSpace(req.SuiteID) == "" {
		return notoapi.BenchRunResult{}, notoapi.NewError(notoapi.CodeInvalidRequest, "suite_id required", nil)
	}
	if req.AgentID == "" {
		req.AgentID = os.Getenv("NOTO_AGENT_ID")
	}
	if os.Getenv("NOTO_BENCH_INTEGRATION_ONLY") == "1" {
		req.IntegrationOnly = true
	}
	manifest := s.buildRunManifest(req)
	gpu := req.GPU
	if gpu == "" {
		gpu = platformbench.DefaultGPU(s.currentCfg().Compute.Modal.GPU)
	}
	res, err := s.benchRunner().Run(ctx, platformbench.RunRequest{
		Manifest:        manifest,
		IntegrationOnly: req.IntegrationOnly,
		GPU:             gpu,
	})
	if err != nil {
		return notoapi.BenchRunResult{}, notoapi.NewError(notoapi.CodeInternal, err.Error(), nil)
	}
	return notoapi.BenchRunResult{
		SchemaVersion:                schemaBenchRunV1,
		RunID:                        res.RunID,
		ArtifactDir:                  res.ArtifactDir,
		EstimatedCostUSD:             res.EstimatedCostUSD,
		CostPerProcessedAudioHourUSD: res.CostPerProcessedAudioHourUSD,
		UnattributedPct:              res.UnattributedPct,
		TraceValid:                   res.TraceValid,
		GPUBusyPct:                   res.GPUBusyPct,
		GPUIdleCostPerAudioHourUSD:   res.GPUIdleCostPerAudioHourUSD,
	}, nil
}

func (s *Service) BenchCompare(ctx context.Context, req notoapi.BenchCompareRequest) (notoapi.BenchCompareResult, error) {
	cmp, err := s.benchRunner().CompareRuns(ctx, req.BaselineRunID, req.CandidateRunID, corebench.DefaultGateOpts())
	if err != nil {
		return notoapi.BenchCompareResult{}, notoapi.NewError(notoapi.CodeInternal, err.Error(), nil)
	}
	var gpu *notoapi.BenchCompareGPU
	if cmp.GPU != nil {
		gpu = &notoapi.BenchCompareGPU{
			BaselineBusyPct:                  cmp.GPU.BaselineBusyPct,
			CandidateBusyPct:                 cmp.GPU.CandidateBusyPct,
			BaselineIdleCostPerAudioHourUSD:  cmp.GPU.BaselineIdleCostPerAudioHourUSD,
			CandidateIdleCostPerAudioHourUSD: cmp.GPU.CandidateIdleCostPerAudioHourUSD,
			DeltaIdleCostPerAudioHourUSD:     cmp.GPU.DeltaIdleCostPerAudioHourUSD,
		}
	}
	var scale *notoapi.BenchCompareScale
	if cmp.Scale != nil {
		scale = &notoapi.BenchCompareScale{
			BaselineFloorUSDPerAudioHour:  cmp.Scale.BaselineFloorUSDPerAudioHour,
			CandidateFloorUSDPerAudioHour: cmp.Scale.CandidateFloorUSDPerAudioHour,
			DeltaFloorUSDPerAudioHour:     cmp.Scale.DeltaFloorUSDPerAudioHour,
			TargetReachableByScale:        cmp.Scale.TargetReachableByScale,
		}
	}
	return notoapi.BenchCompareResult{
		SchemaVersion:      cmp.SchemaVersion,
		BaselineRunID:      cmp.BaselineRunID,
		CandidateRunID:     cmp.CandidateRunID,
		Comparable:         cmp.Comparable,
		IncomparableReason: cmp.IncomparableReason,
		Decision:           string(cmp.Decision),
		Cost: notoapi.BenchCompareCost{
			BaselineCostPerProcessedAudioHourUSD:  cmp.Cost.BaselineCostPerProcessedAudioHourUSD,
			CandidateCostPerProcessedAudioHourUSD: cmp.Cost.CandidateCostPerProcessedAudioHourUSD,
			DeltaPct:                              cmp.Cost.DeltaPct,
			MeetsMDE:                              cmp.Cost.MeetsMDE,
			WaterfallDeltasUSD:                    cmp.Cost.WaterfallDeltasUSD,
		},
		Quality:             cmp.Quality,
		GuardrailsFailed:    cmp.GuardrailsFailed,
		GuardrailsSkipped:   cmp.GuardrailsSkipped,
		NextExperiment:      cmp.NextExperiment,
		AttributionInsights: cmp.AttributionInsights,
		GPU:                 gpu,
		Scale:               scale,
	}, nil
}

func (s *Service) BenchLedgerWinners(ctx context.Context, opts notoapi.BenchLedgerWinnersOpts) (notoapi.BenchLedgerWinnersResult, error) {
	_ = ctx
	winners, err := s.benchRunner().Ledger.Winners()
	if err != nil {
		return notoapi.BenchLedgerWinnersResult{}, notoapi.NewError(notoapi.CodeInternal, err.Error(), nil)
	}
	var out []notoapi.BenchLedgerWinner
	for k, e := range winners {
		if opts.Profile != "" && k.Profile != opts.Profile {
			continue
		}
		if opts.Mode != "" && k.Mode != opts.Mode {
			continue
		}
		out = append(out, notoapi.BenchLedgerWinner{
			RunID:                        e.RunID,
			ExecutionProfile:             e.ExecutionProfile,
			OperatingMode:                e.OperatingMode,
			CostPerProcessedAudioHourUSD: e.CostPerProcessedAudioHourUSD,
		})
	}
	return notoapi.BenchLedgerWinnersResult{Winners: out}, nil
}

func (s *Service) BenchLedgerAppend(ctx context.Context, req notoapi.BenchLedgerAppendRequest) (notoapi.BenchLedgerAppendResult, error) {
	_ = ctx
	if req.AgentID == "" {
		req.AgentID = os.Getenv("NOTO_AGENT_ID")
	}
	entry, err := s.benchRunner().LedgerAppend(platformbench.LedgerAppendRequest{
		RunID:                req.RunID,
		BaselineRunID:        req.BaselineRunID,
		Decision:             req.Decision,
		HypothesisID:         req.HypothesisID,
		ProgramStep:          req.ProgramStep,
		AgentID:              req.AgentID,
		HistoricalUnverified: req.HistoricalUnverified,
	})
	if err != nil {
		return notoapi.BenchLedgerAppendResult{}, notoapi.NewError(notoapi.CodeInternal, err.Error(), nil)
	}
	return notoapi.BenchLedgerAppendResult{
		SchemaVersion: entry.SchemaVersion,
		RunID:         entry.RunID,
		Decision:      entry.Decision,
	}, nil
}

func (s *Service) BenchDatasetList(ctx context.Context) (notoapi.BenchDatasetListResult, error) {
	_ = ctx
	suites := platformbench.ListSuites()
	out := make([]notoapi.BenchDatasetSuite, len(suites))
	for i, s := range suites {
		out[i] = notoapi.BenchDatasetSuite{
			ID: s.ID, ModalSuite: s.ModalSuite, Quick: s.Quick,
			Hours: s.Hours, Description: s.Description,
		}
	}
	return notoapi.BenchDatasetListResult{Suites: out}, nil
}

// BenchRetrace re-derives a completed run's trace_summary.json + compute_audit.json
// from its stored raw artifacts using the current attribution logic (no Modal/GPU
// re-run), then returns the refreshed audit view. Used to apply a trace-logic fix
// (e.g. the A6a diar split) to historical runs cheaply.
func (s *Service) BenchRetrace(ctx context.Context, runID string) (notoapi.BenchAuditResult, error) {
	if strings.TrimSpace(runID) == "" {
		return notoapi.BenchAuditResult{}, notoapi.NewError(notoapi.CodeInvalidRequest, "run_id required", nil)
	}
	if _, err := s.benchRunner().Retrace(runID); err != nil {
		return notoapi.BenchAuditResult{}, notoapi.NewError(notoapi.CodeInternal, err.Error(), nil)
	}
	return s.BenchAudit(ctx, runID)
}

// BenchScale projects the $/audio-hour cost curve toward a target from the runs
// already on disk (no GPU): it fits the per-run fixed overhead vs per-audio-hour
// marginal split and reports whether the target is reachable by scaling audio or
// needs a utilization/batching/work-reduction win.
func (s *Service) BenchScale(ctx context.Context, req notoapi.BenchScaleRequest) (notoapi.BenchScaleResult, error) {
	_ = ctx
	proj, err := s.benchRunner().ScaleProjection(req.TargetCostPerAudioHourUSD, req.AudioHours)
	if err != nil {
		return notoapi.BenchScaleResult{}, notoapi.NewError(notoapi.CodeInternal, err.Error(), nil)
	}
	points := make([]notoapi.BenchScalePoint, len(proj.Points))
	for i, p := range proj.Points {
		points[i] = notoapi.BenchScalePoint{
			AudioHours: p.AudioHours, CostUSD: p.CostUSD, CostPerAudioHourUSD: p.CostPerAudioHourUSD,
		}
	}
	var readiness *notoapi.BenchScaleReadiness
	if proj.Readiness != nil {
		readiness = &notoapi.BenchScaleReadiness{
			Ready:                    proj.Readiness.Ready,
			BusyPct:                  proj.Readiness.BusyPct,
			MinBusyPct:               proj.Readiness.MinBusyPct,
			QualityWithinGuardrails:  proj.Readiness.QualityWithinGuardrails,
			CostPerAudioHourAt10hUSD: proj.Readiness.CostPerAudioHourAt10hUSD,
			Reasons:                  proj.Readiness.Reasons,
		}
	}
	return notoapi.BenchScaleResult{
		SchemaVersion:           proj.SchemaVersion,
		FixedUSD:                proj.FixedUSD,
		MarginalUSDPerAudioHour: proj.MarginalUSDPerAudioHour,
		FloorUSDPerAudioHour:    proj.FloorUSDPerAudioHour,
		TargetUSDPerAudioHour:   proj.TargetUSDPerAudioHour,
		TargetReachableByScale:  proj.TargetReachableByScale,
		HoursToReachTarget:      proj.HoursToReachTarget,
		Points:                  points,
		Readiness:               readiness,
		Notes:                   proj.Notes,
	}, nil
}

// BenchInsights assembles one run's full weighted-KPI snapshot (the
// `noto bench insights` one-command view): it loads the run's metrics + cost,
// folds in the GPU/stage audit and the scale floor, and scores the whole thing
// with the weighted KPI. With no run_id it reports the modal_cuda/batch_queue
// ledger winner, so a developer gets "where do we stand" in one call.
func (s *Service) BenchInsights(ctx context.Context, runID string) (notoapi.BenchInsightsResult, error) {
	_ = ctx
	r := s.benchRunner()
	fromWinner := false
	if strings.TrimSpace(runID) == "" {
		winners, err := r.Ledger.Winners()
		if err != nil {
			return notoapi.BenchInsightsResult{}, notoapi.NewError(notoapi.CodeInternal, err.Error(), nil)
		}
		w, ok := winners[platformbench.WinnerKey{Profile: "modal_cuda", Mode: "batch_queue"}]
		if !ok {
			return notoapi.BenchInsightsResult{}, notoapi.NewError(notoapi.CodeInvalidRequest,
				"no run_id given and no modal_cuda/batch_queue ledger winner exists yet", nil)
		}
		runID, fromWinner = w.RunID, true
	}

	bundle, err := r.Store.LoadRunBundle(runID)
	if err != nil {
		return notoapi.BenchInsightsResult{}, notoapi.NewError(notoapi.CodeInternal, err.Error(), nil)
	}
	wer := bundle.Metrics.Aggregate["wer"]
	der := bundle.Metrics.Aggregate["der"]
	cpwer := bundle.Metrics.Aggregate["cpwer"]

	var busy, idle, meanUtil, peakUtil, unattributed float64
	var topStages []notoapi.BenchAuditStage
	if audit, err := r.AuditRun(runID); err == nil {
		unattributed = audit.UnattributedPct
		if audit.GPU != nil {
			busy, idle = audit.GPU.BusyPct, audit.GPU.IdleCostPerAudioHourUSD
			meanUtil, peakUtil = audit.GPU.MeanUtilizationPct, audit.GPU.PeakUtilizationPct
		}
		for _, st := range audit.TopStages {
			topStages = append(topStages, notoapi.BenchAuditStage{Stage: st.Stage, System: st.System, USD: st.USD})
		}
	}

	weights := corebench.DefaultKPIWeights()
	score := corebench.ScoreRun(corebench.KPIInputs{
		CostPerAudioHourUSD:     bundle.CostPerProcessedHour,
		AnchorCostUSD:           corebench.AnchorCostPerProcessedAudioHourUSD,
		TargetCostUSD:           corebench.StretchTargetCostPerAudioHourUSD,
		WER:                     wer,
		DER:                     der,
		CpWER:                   cpwer,
		BusyPct:                 busy,
		IdleCostPerAudioHourUSD: idle,
	}, weights)

	var floor float64
	var reachable bool
	if proj, err := r.ScaleProjection(0, nil); err == nil {
		floor, reachable = proj.FloorUSDPerAudioHour, proj.TargetReachableByScale
	}

	return notoapi.BenchInsightsResult{
		SchemaVersion:                "bench_insights.v1",
		RunID:                        runID,
		FromWinner:                   fromWinner,
		Score:                        score.Score,
		GuardrailsPass:               score.GuardrailsPass,
		ScoreComponents:              score.Components,
		Weights:                      notoapi.BenchKPIWeights{Cost: weights.Cost, Quality: weights.Quality, Utilization: weights.Utilization},
		Notes:                        score.Notes,
		CostPerProcessedAudioHourUSD: bundle.CostPerProcessedHour,
		AnchorCostUSD:                corebench.AnchorCostPerProcessedAudioHourUSD,
		TargetCostUSD:                corebench.StretchTargetCostPerAudioHourUSD,
		WER:                          wer,
		DER:                          der,
		CpWER:                        cpwer,
		GPUBusyPct:                   busy,
		GPUMeanUtilizationPct:        meanUtil,
		GPUPeakUtilizationPct:        peakUtil,
		GPUIdleCostPerAudioHourUSD:   idle,
		UnattributedPct:              unattributed,
		AttributionWithinGuardrail:   unattributed < 2,
		TopStages:                    topStages,
		MarginalFloorUSDPerAudioHour: floor,
		TargetReachableByScale:       reachable,
	}, nil
}

func (s *Service) BenchAudit(ctx context.Context, runID string) (notoapi.BenchAuditResult, error) {
	_ = ctx
	if strings.TrimSpace(runID) == "" {
		return notoapi.BenchAuditResult{}, notoapi.NewError(notoapi.CodeInvalidRequest, "run_id required", nil)
	}
	audit, err := s.benchRunner().AuditRun(runID)
	if err != nil {
		return notoapi.BenchAuditResult{}, notoapi.NewError(notoapi.CodeInternal, err.Error(), nil)
	}
	stages := make([]notoapi.BenchAuditStage, len(audit.TopStages))
	for i, stage := range audit.TopStages {
		stages[i] = notoapi.BenchAuditStage{Stage: stage.Stage, System: stage.System, USD: stage.USD}
	}
	var gpu *notoapi.BenchAuditGPU
	if audit.GPU != nil {
		gpu = &notoapi.BenchAuditGPU{
			MeanUtilizationPct:      audit.GPU.MeanUtilizationPct,
			PeakUtilizationPct:      audit.GPU.PeakUtilizationPct,
			BusyPct:                 audit.GPU.BusyPct,
			PeakVRAMMB:              audit.GPU.PeakVRAMMB,
			IdleCostUSD:             audit.GPU.IdleCostUSD,
			IdleCostPerAudioHourUSD: audit.GPU.IdleCostPerAudioHourUSD,
		}
	}
	return notoapi.BenchAuditResult{
		SchemaVersion:   audit.SchemaVersion,
		RunID:           audit.RunID,
		ArtifactDir:     audit.ArtifactDir,
		TotalUSD:        audit.TotalUSD,
		ComputeUSD:      audit.ComputeUSD,
		UnattributedPct: audit.UnattributedPct,
		TopStages:       stages,
		GPU:             gpu,
	}, nil
}
