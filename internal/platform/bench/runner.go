package bench

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/lukasstrickler/noto/internal/core/artifacts"
	corebench "github.com/lukasstrickler/noto/internal/core/bench"
)

// Runner orchestrates preflight validation, execution, and post-run checks (§22.14 subset).
type Runner struct {
	Store  *Store
	Ledger *Ledger
	Modal  *ModalRunner
	Spend  *SpendLog
}

// AuditResult is the compact human/agent view of a run's compute attribution.
type AuditResult struct {
	SchemaVersion   string                    `json:"schema_version"`
	RunID           string                    `json:"run_id"`
	ArtifactDir     string                    `json:"artifact_dir"`
	TotalUSD        float64                   `json:"total_usd"`
	ComputeUSD      float64                   `json:"compute_usd"`
	UnattributedPct float64                   `json:"unattributed_pct"`
	TopStages       []corebench.StageCost     `json:"top_stages"`
	Waterfall       corebench.CostWaterfall   `json:"waterfall"`
	GPU             *corebench.GPUUtilization `json:"gpu,omitempty"`
}

const SchemaBenchAuditV1 = "bench_audit.v1"

func NewRunner(store *Store) *Runner {
	return &Runner{Store: store, Ledger: NewLedger(store), Spend: NewSpendLog(store)}
}

func (r *Runner) AuditRun(runID string) (AuditResult, error) {
	trace, err := r.Store.LoadTraceSummary(runID)
	if err != nil {
		return AuditResult{}, err
	}
	stages := append([]corebench.StageCost(nil), trace.ComputeByStage...)
	sort.SliceStable(stages, func(i, j int) bool {
		if stages[i].USD == stages[j].USD {
			return stages[i].Stage < stages[j].Stage
		}
		return stages[i].USD > stages[j].USD
	})
	if len(stages) > 3 {
		stages = stages[:3]
	}
	return AuditResult{
		SchemaVersion:   SchemaBenchAuditV1,
		RunID:           runID,
		ArtifactDir:     r.Store.RunDir(runID),
		TotalUSD:        trace.CostWaterfall.TotalUSD,
		ComputeUSD:      trace.CostWaterfall.ComputeUSD,
		UnattributedPct: trace.CostWaterfall.UnattributedPct,
		TopStages:       stages,
		Waterfall:       trace.CostWaterfall,
		GPU:             trace.GPU,
	}, nil
}

// Retrace re-derives trace_summary.json and compute_audit.json for an already
// completed run from its stored raw artifacts (summary.json, hyps/, setup.log),
// using the CURRENT trace-attribution logic — no Modal/GPU re-run. This lets a
// fix to the attribution math (e.g. the A6a diar split, which falls back to the
// per-meeting diar wall when the pyannote server's stages_ms stderr wasn't
// captured) be applied to historical runs for the cost of reading a few files.
// metrics.json and manifest.json are left untouched; retrace only rewrites the
// cost/trace view.
func (r *Runner) Retrace(runID string) (corebench.TraceSummary, error) {
	dir := r.Store.RunDir(runID)
	summary, err := LoadModalSummary(filepath.Join(dir, "summary.json"))
	if err != nil {
		return corebench.TraceSummary{}, fmt.Errorf("load summary.json: %w", err)
	}
	if summary.RunID == "" {
		summary.RunID = runID
	}
	hyps, err := loadMeetingHyps(filepath.Join(dir, "hyps"))
	if err != nil {
		return corebench.TraceSummary{}, fmt.Errorf("load hyps: %w", err)
	}
	setupLog, _ := os.ReadFile(filepath.Join(dir, "setup.log"))
	pyannote := extractPyannoteStderr(string(setupLog))

	// Best-effort: the manifest only supplies execution_profile/operating_mode
	// for the re-derived audit; a missing or malformed one just leaves them empty.
	var manifest corebench.RunManifest
	_ = readJSON(filepath.Join(dir, "manifest.json"), &manifest)

	trace := BuildTraceSummary(summary, hyps, ParsePyannoteStderr(pyannote), DefaultPyannoteStageMap)
	audit, err := MapSummaryToAudit(summary, trace, manifest.ExecutionProfile, manifest.OperatingMode)
	if err != nil {
		return trace, fmt.Errorf("map audit: %w", err)
	}
	if err := r.Store.WriteJSON(runID, "trace_summary.json", trace); err != nil {
		return trace, err
	}
	if err := r.Store.WriteJSON(runID, "compute_audit.json", audit); err != nil {
		return trace, err
	}
	return trace, nil
}

// PreflightRequest is bench preflight input.
type PreflightRequest struct {
	Manifest corebench.RunManifest
}

// PreflightResponse is bench_preflight.v1.
type PreflightResponse struct {
	SchemaVersion      string   `json:"schema_version"`
	SuiteID            string   `json:"suite_id"`
	Tier               string   `json:"tier"`
	BudgetUSDCap       float64  `json:"budget_usd_cap"`
	ProjectedCostUSD   float64  `json:"projected_cost_usd,omitempty"`
	ComparableToWinner bool     `json:"comparable_to_winner"`
	Blockers           []string `json:"blockers"`
	Warnings           []string `json:"warnings,omitempty"`
}

const SchemaPreflightV1 = "bench_preflight.v1"

func (r *Runner) Preflight(ctx context.Context, req PreflightRequest) (PreflightResponse, error) {
	_ = ctx
	resp := PreflightResponse{
		SchemaVersion:      SchemaPreflightV1,
		SuiteID:            req.Manifest.SuiteID,
		Tier:               req.Manifest.Tier,
		BudgetUSDCap:       req.Manifest.BudgetUSDCap,
		Blockers:           []string{},
		ComparableToWinner: true,
	}
	if corebench.RejectGateKnobBundle(req.Manifest) {
		resp.Blockers = append(resp.Blockers, "knob_bundle_on_gate")
		resp.ComparableToWinner = false
	}
	// A live (billable) run shells out to the Modal interpreter and the user's
	// Modal credentials; gate it on those being present so the runner makes the
	// correct call — a real run only when configured, else an actionable blocker
	// instead of a cryptic subprocess failure. Integration-only runs use a local
	// fixture and need none of it.
	if !req.Manifest.IntegrationOnly && r.Modal != nil {
		if pre := CheckModalPrereqs(r.Modal.RepoRoot); !pre.Ready() {
			resp.Blockers = append(resp.Blockers, pre.Blockers()...)
			resp.ComparableToWinner = false
		}
	}
	return resp, nil
}

// CompareRuns loads two runs and produces compare.json.
func (r *Runner) CompareRuns(ctx context.Context, baselineID, candidateID string, opts corebench.GateOpts) (corebench.CompareResult, error) {
	_ = ctx
	base, err := r.Store.LoadRunBundle(baselineID)
	if err != nil {
		return corebench.CompareResult{}, err
	}
	cand, err := r.Store.LoadRunBundle(candidateID)
	if err != nil {
		return corebench.CompareResult{}, err
	}
	cmp := corebench.Compare(base, cand, opts)
	if err := r.Store.WriteCompare(candidateID, cmp); err != nil {
		return cmp, err
	}
	return cmp, nil
}

// AppendLedger validates artifact tree then appends.
func (r *Runner) AppendLedger(entry LedgerEntry) error {
	if err := r.Store.ValidateArtifactTree(entry.RunID); err != nil {
		return fmt.Errorf("artifact tree: %w", err)
	}
	return r.Ledger.Append(entry)
}

// MapSummaryToAudit builds compute_audit.v1 from modal summary + trace summary.
func MapSummaryToAudit(summary ModalSummary, trace corebench.TraceSummary, profile, mode string) (map[string]any, error) {
	cph := summary.CostPerAudioHourUSD
	if cph == 0 && summary.ProcessedAudioHours > 0 {
		cph = summary.EstimatedCostUSD / summary.ProcessedAudioHours
	}
	audit := map[string]any{
		"schema_version":    artifacts.SchemaComputeAuditV1,
		"run_id":            summary.RunID,
		"execution_profile": profile,
		"operating_mode":    mode,
		"hardware": map[string]any{
			"gpu": summary.GPU, "cpu_cores": summary.CPUCores, "memory_gib": summary.MemoryGiB,
		},
		"cost": map[string]any{
			"estimated_cost_usd":                summary.EstimatedCostUSD,
			"cost_per_processed_audio_hour_usd": cph,
			"unattributed_pct":                  trace.CostWaterfall.UnattributedPct,
		},
		"trace_summary_ref": "trace_summary.json",
	}
	return audit, nil
}

// Run executes preflight then modal benchmark (or integration fixture).
//
// §22.14 order: CheckSpendCaps → Preflight → reject knob bundle → launch GPU.
// Spend caps and accounting apply only to GPU-billed runs; integration-only
// fixtures cost nothing, so they neither check nor record.
func (r *Runner) Run(ctx context.Context, req RunRequest) (RunResponse, error) {
	billable := !req.IntegrationOnly
	if billable && r.Spend != nil {
		projected := projectedSpend(req.Manifest)
		if err := r.Spend.CheckCaps(req.Manifest.AgentID, req.Manifest.HypothesisID, projected); err != nil {
			return RunResponse{}, err
		}
	}
	pf, err := r.Preflight(ctx, PreflightRequest{Manifest: req.Manifest})
	if err != nil {
		return RunResponse{}, err
	}
	if len(pf.Blockers) > 0 {
		return RunResponse{}, fmt.Errorf("preflight blocked: %v", pf.Blockers)
	}
	if r.Modal == nil {
		return RunResponse{}, fmt.Errorf("modal runner not configured")
	}
	resp, err := r.Modal.Run(ctx, req)
	if err != nil {
		return resp, err
	}
	if billable && r.Spend != nil {
		_ = r.Spend.Record(SpendRecord{
			RunID:            resp.RunID,
			AgentID:          req.Manifest.AgentID,
			HypothesisID:     req.Manifest.HypothesisID,
			Tier:             req.Manifest.Tier,
			EstimatedCostUSD: resp.EstimatedCostUSD,
		})
	}
	return resp, nil
}

// projectedSpend is the EXPECTED cost used for the pre-launch agent/hypothesis
// daily-cap check — anchor $/audio-hr × the suite's audio hours. It deliberately
// does NOT use the tier ceiling: a whole-corpus anchor run costs ~$0.22 but its
// baseline_variance ceiling is $15, which would block every such run against the
// $5/day agent cap. The tier ceiling is still enforced mid-run by the runner as
// a hard stop, and recorded ACTUAL spend accumulates against the daily cap after
// the run — so the daily cap gates on plausible spend, not the worst case.
// Falls back to the tier cap only when the suite's audio hours are unknown.
func projectedSpend(m corebench.RunManifest) float64 {
	if hours := SuiteAudioHours(m.SuiteID); hours > 0 {
		est := corebench.Estimate(corebench.EstimateInput{
			SuiteID:    m.SuiteID,
			Tier:       m.Tier,
			AudioHours: hours,
			TierCapUSD: EffectiveBudget(m.Tier, m.BudgetUSDCap),
		})
		if est.ProjectedCostUSD > 0 {
			return est.ProjectedCostUSD
		}
	}
	if m.BudgetUSDCap > 0 {
		return m.BudgetUSDCap
	}
	return EffectiveBudget(m.Tier, 0)
}

// LedgerAppendRequest is input for ledger append.
type LedgerAppendRequest struct {
	RunID                string
	BaselineRunID        string
	Decision             string
	HypothesisID         string
	ProgramStep          string
	AgentID              string
	HistoricalUnverified bool
}

// LedgerAppend validates artifacts and appends ledger entry.
// hasWinner reports whether a ledger winner already exists for a (profile, mode)
// tuple — the gate that distinguishes seeding the first baseline from adopting
// an improvement over an existing winner.
func (r *Runner) hasWinner(profile, mode string) bool {
	winners, err := r.Ledger.Winners()
	if err != nil {
		return false
	}
	_, ok := winners[WinnerKey{Profile: profile, Mode: mode}]
	return ok
}

func (r *Runner) LedgerAppend(req LedgerAppendRequest) (LedgerEntry, error) {
	if req.Decision == "" {
		return LedgerEntry{}, fmt.Errorf("decision required")
	}
	if req.Decision == "adopt" && req.HistoricalUnverified {
		return LedgerEntry{}, fmt.Errorf("cannot adopt historical_unverified entry")
	}
	bundle, err := r.Store.LoadRunBundle(req.RunID)
	if err != nil {
		return LedgerEntry{}, err
	}
	// Seeding the FIRST reference winner for a (profile, mode) tuple is the one
	// adoption with no compare.json: there is no prior winner to beat, so the
	// improvement-bearing compare cannot exist. Gated tightly — only when the
	// caller marks it `baseline` AND no winner exists yet — so a real
	// optimization can never bypass the compare gate.
	seedBaseline := req.Decision == "adopt" && req.HypothesisID == "baseline" &&
		!r.hasWinner(bundle.Manifest.ExecutionProfile, bundle.Manifest.OperatingMode)
	if seedBaseline {
		if err := r.Store.ValidateArtifactTreeExcept(req.RunID, "compare.json"); err != nil {
			return LedgerEntry{}, fmt.Errorf("artifact tree: %w", err)
		}
	} else {
		if err := r.Store.ValidateArtifactTree(req.RunID); err != nil {
			return LedgerEntry{}, fmt.Errorf("artifact tree: %w", err)
		}
		if req.Decision == "adopt" {
			cmp, err := r.Store.LoadCompare(req.RunID)
			if err != nil {
				return LedgerEntry{}, err
			}
			if cmp.CandidateRunID != req.RunID {
				return LedgerEntry{}, fmt.Errorf("compare candidate mismatch: %s", cmp.CandidateRunID)
			}
			if cmp.Decision != corebench.DecisionAdopt && cmp.Decision != corebench.DecisionNeedsConfirm {
				return LedgerEntry{}, fmt.Errorf("compare decision %q is not adoptable", cmp.Decision)
			}
		}
	}
	entry := LedgerEntry{
		SchemaVersion:                SchemaLedgerEntryV1,
		RunID:                        req.RunID,
		BaselineRunID:                req.BaselineRunID,
		HypothesisID:                 req.HypothesisID,
		ProgramStep:                  req.ProgramStep,
		ExecutionProfile:             bundle.Manifest.ExecutionProfile,
		OperatingMode:                bundle.Manifest.OperatingMode,
		Decision:                     req.Decision,
		AgentID:                      req.AgentID,
		HistoricalUnverified:         req.HistoricalUnverified,
		CostPerProcessedAudioHourUSD: bundle.CostPerProcessedHour,
		ArtifactDir:                  r.Store.RunDir(req.RunID),
	}
	if err := r.Store.WriteJSON(req.RunID, "ledger_entry.json", entry); err != nil {
		return LedgerEntry{}, err
	}
	if err := r.Ledger.Append(entry); err != nil {
		return LedgerEntry{}, err
	}
	return entry, nil
}
