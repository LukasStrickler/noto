package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/lukasstrickler/noto/internal/transport/notoapi"
)

func (a *app) runBench(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(a.errOut, "noto bench [insights|estimate|run|preflight|compare|audit|scale|dataset|ledger|help]")
		return 64
	}
	switch args[0] {
	case "help", "--help", "-h":
		fmt.Fprint(a.out, `noto bench — measurement spine (Program A)

Usage:
  noto bench insights [--run <run_id>] [--json]   # one-command weighted-KPI snapshot
  noto bench repair --run <run_id> [--json]       # B7 dry-run repair preview (candidates + cost)
  noto bench calibration --run <run_id> [--json]  # B6 confidence calibration (ECE, capture, admissibility)
  noto bench overlap --run <run_id> [--json]      # overlap repair: cost + addressable diarization error
  noto bench estimate --suite <id> [--tier <tier>] [--json]
  noto bench run --suite <id> [--tier <tier>] [--mode <mode>] [--integration-only] [--json]
  noto bench preflight --suite <id> --tier <tier> --mode <mode> [--json]
  noto bench compare --baseline <run_id> --candidate <run_id> [--json]
  noto bench audit --run <run_id> [--json]
  noto bench retrace --run <run_id> [--json]
  noto bench scale [--target <usd_per_audio_hr>] [--hours 1,10,100] [--json]
  noto bench dataset list [--json]
  noto bench ledger winners [--profile modal_cuda] [--mode batch_queue] [--json]
  noto bench ledger append --run <run_id> --decision <decision> [--json]

Set NOTO_AGENT_ID for spend accounting on runs.

`)
		return 0
	case "insights":
		return a.runBenchInsights(args[1:])
	case "repair":
		return a.runBenchRepair(args[1:])
	case "calibration":
		return a.runBenchCalibration(args[1:])
	case "overlap":
		return a.runBenchOverlap(args[1:])
	case "estimate":
		return a.runBenchEstimate(args[1:])
	case "run":
		return a.runBenchRun(args[1:])
	case "preflight":
		return a.runBenchPreflight(args[1:])
	case "compare":
		return a.runBenchCompare(args[1:])
	case "audit":
		return a.runBenchAudit(args[1:])
	case "retrace":
		return a.runBenchRetrace(args[1:])
	case "scale":
		return a.runBenchScale(args[1:])
	case "dataset":
		return a.runBenchDataset(args[1:])
	case "ledger":
		return a.runBenchLedger(args[1:])
	default:
		fmt.Fprintf(a.errOut, "noto bench: unknown subcommand %q\n", args[0])
		return 64
	}
}

// runBenchInsights is the one-command development snapshot: the weighted KPI
// score + components, cost vs anchor/target, quality vs guardrails, GPU busy/idle
// (idle = money not well spent), the top cost stages, and the asymptotic floor —
// for a run, or the ledger winner when --run is omitted.
func (a *app) runBenchInsights(args []string) int {
	fs := flag.NewFlagSet("bench insights", flag.ContinueOnError)
	run := fs.String("run", "", "run id (default: modal_cuda/batch_queue ledger winner)")
	jsonOut := fs.Bool("json", false, "JSON output")
	if err := fs.Parse(args); err != nil {
		return 64
	}
	ctx := context.Background()
	client, closeFn, code := a.connect(ctx)
	if code != 0 {
		return code
	}
	defer closeFn()
	res, err := client.BenchInsights(ctx, *run)
	if err != nil {
		return a.errExit(err)
	}
	if *jsonOut {
		return a.emitJSON(res)
	}
	src := res.RunID
	if res.FromWinner {
		src += "  (ledger winner)"
	}
	gate := "PASS"
	if !res.GuardrailsPass {
		gate = "FAIL"
	}
	fmt.Fprintf(a.out, "bench insights — %s\n", src)
	fmt.Fprintf(a.out, "  weighted KPI: %.1f/100   guardrails: %s\n", res.Score, gate)
	fmt.Fprintf(a.out, "    components: cost %.2f · quality %.2f · utilization %.2f   (weights %.2f/%.2f/%.2f)\n",
		res.ScoreComponents["cost"], res.ScoreComponents["quality"], res.ScoreComponents["utilization"],
		res.Weights.Cost, res.Weights.Quality, res.Weights.Utilization)
	fmt.Fprintf(a.out, "  cost:    $%.5f/audio-hr   (anchor $%.4f · target $%.4f)\n",
		res.CostPerProcessedAudioHourUSD, res.AnchorCostUSD, res.TargetCostUSD)
	if res.WER > 0 || res.DER > 0 || res.CpWER > 0 {
		fmt.Fprintf(a.out, "  quality: WER %.3f · DER %.3f · cpWER %.3f\n", res.WER, res.DER, res.CpWER)
	}
	if res.GPUBusyPct > 0 || res.GPUIdleCostPerAudioHourUSD > 0 {
		fmt.Fprintf(a.out, "  gpu:     busy %.1f%%   idle $%.4f/audio-hr (money not well spent)\n",
			res.GPUBusyPct, res.GPUIdleCostPerAudioHourUSD)
		if res.GPUMeanUtilizationPct > 0 || res.GPUPeakUtilizationPct > 0 {
			fmt.Fprintf(a.out, "           util mean %.1f%% / peak %.1f%% (the gap is headroom to pack)\n",
				res.GPUMeanUtilizationPct, res.GPUPeakUtilizationPct)
		}
	}
	attn := "within guardrail"
	if !res.AttributionWithinGuardrail {
		attn = "OVER 2% guardrail — bill not fully explained"
	}
	fmt.Fprintf(a.out, "  track:   unattributed %.2f%% (%s) — how much of the bill we can explain\n",
		res.UnattributedPct, attn)
	if res.MarginalFloorUSDPerAudioHour > 0 {
		reach := "needs a utilization/batching/work-reduction win"
		if res.TargetReachableByScale {
			reach = "reachable by scaling audio"
		}
		fmt.Fprintf(a.out, "  scale:   marginal floor $%.4f/audio-hr — $%.4f target %s\n",
			res.MarginalFloorUSDPerAudioHour, res.TargetCostUSD, reach)
	}
	for _, st := range res.TopStages {
		fmt.Fprintf(a.out, "    stage %-14s $%.4f\n", st.Stage, st.USD)
	}
	for _, n := range res.Notes {
		fmt.Fprintf(a.out, "  • %s\n", n)
	}
	return 0
}

// runBenchRepair shows the B7 dry-run repair preview for a run: low-confidence
// words grouped into candidate spans, planned under a repair budget — what a second
// pass would attempt and what it would cost. Read-only; no transcript writes.
func (a *app) runBenchRepair(args []string) int {
	fs := flag.NewFlagSet("bench repair", flag.ContinueOnError)
	run := fs.String("run", "", "run id (required)")
	jsonOut := fs.Bool("json", false, "JSON output")
	if err := fs.Parse(args); err != nil {
		return 64
	}
	if *run == "" {
		fmt.Fprintln(a.errOut, "noto bench repair --run <run_id>")
		return 64
	}
	ctx := context.Background()
	client, closeFn, code := a.connect(ctx)
	if code != 0 {
		return code
	}
	defer closeFn()
	res, err := client.BenchRepair(ctx, *run)
	if err != nil {
		return a.errExit(err)
	}
	if *jsonOut {
		return a.emitJSON(res)
	}
	fmt.Fprintf(a.out, "bench repair (dry-run preview) — %s\n", res.RunID)
	if !res.HasConfidence {
		fmt.Fprintf(a.out, "  no word confidence on this run — re-run with `--knob confidence=1` so repair\n")
		fmt.Fprintf(a.out, "  has per-word P(correct) to select candidates from.\n")
		return 0
	}
	fmt.Fprintf(a.out, "  %d meetings · %.0fs speech · confidence<%.2f flagged\n",
		res.Meetings, res.TotalSpeechSec, res.ConfidenceThreshold)
	fmt.Fprintf(a.out, "  candidates: %d spans (%.1fs)   budget %.1fs   attempt %.1fs   skipped(budget) %.1fs\n",
		res.CandidateSpans, res.CandidateSec, res.BudgetSec, res.AttemptSec, res.SkippedBudgetSec)
	fmt.Fprintf(a.out, "  projected re-decode cost: $%.5f  (dry-run only — no transcript writes until B7 gate passes)\n",
		res.ProjectedCostUSD)
	return 0
}

// runBenchCalibration scores a run's word-confidence calibration (B6): how well the
// model's confidence predicts its own errors, and whether that signal is good
// enough to drive repair spend (the admissibility gate). Read-only, GPU-free.
func (a *app) runBenchCalibration(args []string) int {
	fs := flag.NewFlagSet("bench calibration", flag.ContinueOnError)
	run := fs.String("run", "", "run id (required)")
	jsonOut := fs.Bool("json", false, "JSON output")
	if err := fs.Parse(args); err != nil {
		return 64
	}
	if *run == "" {
		fmt.Fprintln(a.errOut, "noto bench calibration --run <run_id>")
		return 64
	}
	ctx := context.Background()
	client, closeFn, code := a.connect(ctx)
	if code != 0 {
		return code
	}
	defer closeFn()
	res, err := client.BenchCalibration(ctx, *run)
	if err != nil {
		return a.errExit(err)
	}
	if *jsonOut {
		return a.emitJSON(res)
	}
	fmt.Fprintf(a.out, "bench calibration (B6) — %s\n", res.RunID)
	if !res.HasConfidence {
		fmt.Fprintf(a.out, "  no word confidence on this run — re-run with `--knob confidence=1`.\n")
		return 0
	}
	admit := "ADMISSIBLE"
	if !res.SignalAdmissible {
		admit = "NOT admissible"
	}
	fmt.Fprintf(a.out, "  %d scored words (%d errors)\n", res.Words, res.Errors)
	fmt.Fprintf(a.out, "  ECE %.4f · Brier %.4f · risk-coverage AUC %.4f\n", res.ECE, res.Brier, res.RiskCoverageAUC)
	fmt.Fprintf(a.out, "  bottom-decile capture %.3f (random %.2f, lift %+.3f) — does low confidence find errors?\n",
		res.BottomDecileCapture, 0.10, res.CaptureLiftOverRandom)
	fmt.Fprintf(a.out, "  high-confidence error rate %.4f\n", res.HighConfErrorRate)
	fmt.Fprintf(a.out, "  repair ceiling: oracle-fixing the bottom %.0f%% confidence words → error rate %.4f → %.4f (fix %d errors)\n",
		res.RepairFraction*100, res.CurrentErrorRate, res.CeilingErrorRate, res.RepairFixableErrors)
	fmt.Fprintf(a.out, "  signal for repair spend: %s\n", admit)
	for _, r := range res.Reasons {
		fmt.Fprintf(a.out, "    · %s\n", r)
	}
	return 0
}

// runBenchOverlap analyzes a run's hard-overlap cost + addressable diarization error
// (the diarization side of the repair system): how much of the meeting is genuine
// >=2-speaker overlap, how much DER error lives there, and what repairing ONLY those
// seconds costs vs separating everything. Read-only, GPU-free.
func (a *app) runBenchOverlap(args []string) int {
	fs := flag.NewFlagSet("bench overlap", flag.ContinueOnError)
	run := fs.String("run", "", "run id (required)")
	jsonOut := fs.Bool("json", false, "JSON output")
	if err := fs.Parse(args); err != nil {
		return 64
	}
	if *run == "" {
		fmt.Fprintln(a.errOut, "noto bench overlap --run <run_id>")
		return 64
	}
	ctx := context.Background()
	client, closeFn, code := a.connect(ctx)
	if code != 0 {
		return code
	}
	defer closeFn()
	res, err := client.BenchOverlap(ctx, *run)
	if err != nil {
		return a.errExit(err)
	}
	if *jsonOut {
		return a.emitJSON(res)
	}
	fmt.Fprintf(a.out, "bench overlap (diarization repair) — %s\n", res.RunID)
	if res.Scored == 0 {
		fmt.Fprintf(a.out, "  no scorable meetings (no references matched) — nothing to analyze.\n")
		return 0
	}
	fmt.Fprintf(a.out, "  %d/%d meetings · hard-overlap %.2f%% of speech (%.0fs of %.0fs, peak %d concurrent)\n",
		res.Scored, res.Meetings, res.OverlapFraction*100, res.TotalOverlapSec, res.TotalSpeechSec, res.PeakSpeakers)
	if res.HasDiarization && res.TotalDERErrorSec > 0 {
		fmt.Fprintf(a.out, "  addressable: %.1f%% of diarization error is in overlap regions (%.0fs of %.0fs DER error)\n",
			res.AddressableFraction*100, res.OverlapDERErrorSec, res.TotalDERErrorSec)
	} else {
		fmt.Fprintf(a.out, "  addressable: run has no diarization hyp to score overlap error against\n")
	}
	fmt.Fprintf(a.out, "  cost @ sep %.1f× (base $%.4f/audio-hr): targeted +%.1f%% ($%.4f) vs blanket +%.0f%% ($%.4f) → %.1f× cheaper\n",
		res.SepCostFactor, res.BaseCostPerAudioHourUSD,
		res.TargetedExtraPct, res.TargetedExtraUSD, res.BlanketExtraPct, res.BlanketExtraUSD, res.SavingsFactor)
	return 0
}

func (a *app) runBenchEstimate(args []string) int {
	fs := flag.NewFlagSet("bench estimate", flag.ContinueOnError)
	suite := fs.String("suite", "", "suite id (required)")
	tier := fs.String("tier", "gate", "budget tier")
	mode := fs.String("mode", "batch_queue", "operating mode")
	profile := fs.String("profile", "modal_cuda", "execution profile")
	jsonOut := fs.Bool("json", false, "JSON output")
	if err := fs.Parse(args); err != nil {
		return 64
	}
	if *suite == "" {
		fmt.Fprintln(a.errOut, "noto bench estimate --suite <id>")
		return 64
	}
	ctx := context.Background()
	client, close, code := a.connect(ctx)
	if code != 0 {
		return code
	}
	defer close()
	res, err := client.BenchEstimate(ctx, notoapi.BenchEstimateRequest{
		SuiteID:          *suite,
		Tier:             *tier,
		OperatingMode:    *mode,
		ExecutionProfile: *profile,
	})
	if err != nil {
		return a.errExit(err)
	}
	if *jsonOut {
		return a.emitJSON(res)
	}
	fmt.Fprintf(a.out, "suite: %s tier: %s audio: %.2fh\n", res.SuiteID, res.Tier, res.AudioHours)
	fmt.Fprintf(a.out, "projected: $%.4f (anchor $%.4f/audio-hr) cap $%.2f within_budget: %v\n",
		res.ProjectedCostUSD, res.AnchorCostPerProcessedAudioHourUSD, res.TierBudgetCapUSD, res.WithinBudget)
	fmt.Fprintf(a.out, "target: $%.4f/audio-hr headroom $%.4f/audio-hr → save up to $%.4f at target\n",
		res.TargetCostPerProcessedAudioHourUSD, res.IdleHeadroomPerAudioHourUSD, res.ProjectedHeadroomSavingsUSD)
	for _, n := range res.Notes {
		fmt.Fprintf(a.out, "note: %s\n", n)
	}
	return 0
}

func benchAgentID(flagVal string) string {
	if flagVal != "" {
		return flagVal
	}
	return os.Getenv("NOTO_AGENT_ID")
}

func (a *app) runBenchRun(args []string) int {
	fs := flag.NewFlagSet("bench run", flag.ContinueOnError)
	suite := fs.String("suite", "", "suite id (required)")
	tier := fs.String("tier", "gate", "budget tier")
	mode := fs.String("mode", "batch_queue", "operating mode")
	profile := fs.String("profile", "modal_cuda", "execution profile")
	budget := fs.Float64("budget-usd", 0, "budget cap USD (0 = tier default)")
	hypothesis := fs.String("hypothesis", "", "hypothesis id (H1, …)")
	knob := fs.String("knob", "", "single knob key=value")
	integration := fs.Bool("integration-only", false, "ingest golden fixture (no Modal GPU)")
	agentID := fs.String("agent-id", "", "agent id (default NOTO_AGENT_ID)")
	jsonOut := fs.Bool("json", false, "JSON output")
	if err := fs.Parse(args); err != nil {
		return 64
	}
	if *suite == "" {
		fmt.Fprintln(a.errOut, "noto bench run --suite <id>")
		return 64
	}
	knobs := map[string]string{}
	if *knob != "" {
		k, v, ok := splitKnob(*knob)
		if !ok {
			fmt.Fprintln(a.errOut, "knob must be key=value")
			return 64
		}
		knobs[k] = v
	}
	ctx := context.Background()
	client, close, code := a.connect(ctx)
	if code != 0 {
		return code
	}
	defer close()
	res, err := client.BenchRun(ctx, notoapi.BenchRunRequest{
		SuiteID:          *suite,
		Tier:             *tier,
		OperatingMode:    *mode,
		ExecutionProfile: *profile,
		BudgetUSDCap:     *budget,
		Knobs:            knobs,
		HypothesisID:     *hypothesis,
		IntegrationOnly:  *integration,
		AgentID:          benchAgentID(*agentID),
	})
	if err != nil {
		return a.errExit(err)
	}
	if *jsonOut {
		return a.emitJSON(res)
	}
	fmt.Fprintf(a.out, "run_id: %s trace_valid: %v $/hr: %.4f unattributed: %.2f%%\n",
		res.RunID, res.TraceValid, res.CostPerProcessedAudioHourUSD, res.UnattributedPct)
	if res.GPUBusyPct > 0 || res.GPUIdleCostPerAudioHourUSD > 0 {
		fmt.Fprintf(a.out, "gpu: busy %.1f%% idle $%.4f/audio-hr (headroom toward $0.01 target)\n",
			res.GPUBusyPct, res.GPUIdleCostPerAudioHourUSD)
	}
	return 0
}

func (a *app) runBenchAudit(args []string) int {
	fs := flag.NewFlagSet("bench audit", flag.ContinueOnError)
	runID := fs.String("run", "", "run id")
	jsonOut := fs.Bool("json", false, "JSON output")
	if err := fs.Parse(args); err != nil {
		return 64
	}
	if *runID == "" {
		fmt.Fprintln(a.errOut, "noto bench audit --run <id>")
		return 64
	}
	ctx := context.Background()
	client, close, code := a.connect(ctx)
	if code != 0 {
		return code
	}
	defer close()
	res, err := client.BenchAudit(ctx, *runID)
	if err != nil {
		return a.errExit(err)
	}
	if *jsonOut {
		return a.emitJSON(res)
	}
	a.printBenchAudit(res)
	return 0
}

// runBenchRetrace re-derives a completed run's trace + audit from its stored raw
// artifacts using the current attribution logic (no Modal/GPU re-run), then prints
// the refreshed audit. Use it to apply a trace-logic fix to historical runs.
func (a *app) runBenchRetrace(args []string) int {
	fs := flag.NewFlagSet("bench retrace", flag.ContinueOnError)
	runID := fs.String("run", "", "run id")
	jsonOut := fs.Bool("json", false, "JSON output")
	if err := fs.Parse(args); err != nil {
		return 64
	}
	if *runID == "" {
		fmt.Fprintln(a.errOut, "noto bench retrace --run <id>")
		return 64
	}
	ctx := context.Background()
	client, close, code := a.connect(ctx)
	if code != 0 {
		return code
	}
	defer close()
	res, err := client.BenchRetrace(ctx, *runID)
	if err != nil {
		return a.errExit(err)
	}
	if *jsonOut {
		return a.emitJSON(res)
	}
	a.printBenchAudit(res)
	return 0
}

func (a *app) printBenchAudit(res notoapi.BenchAuditResult) {
	fmt.Fprintf(a.out, "run_id: %s total: $%.4f compute: $%.4f unattributed: %.2f%%\n",
		res.RunID, res.TotalUSD, res.ComputeUSD, res.UnattributedPct)
	for _, stage := range res.TopStages {
		if stage.System != "" {
			fmt.Fprintf(a.out, "%s\t%s\t$%.4f\n", stage.Stage, stage.System, stage.USD)
		} else {
			fmt.Fprintf(a.out, "%s\t$%.4f\n", stage.Stage, stage.USD)
		}
	}
	if g := res.GPU; g != nil {
		fmt.Fprintf(a.out, "gpu: busy %.1f%% mean %.1f%% peak_vram %.0fMB idle $%.4f (headroom $%.4f/audio-hr)\n",
			g.BusyPct, g.MeanUtilizationPct, g.PeakVRAMMB, g.IdleCostUSD, g.IdleCostPerAudioHourUSD)
	}
}

// runBenchScale projects the $/audio-hour cost curve toward a target from the
// runs already on disk — the path-to-$0.01 view: how cost amortizes with scale
// and whether the target is reachable by adding audio or needs a rate win.
func (a *app) runBenchScale(args []string) int {
	fs := flag.NewFlagSet("bench scale", flag.ContinueOnError)
	target := fs.Float64("target", 0, "target $/processed-audio-hour (default $0.01 stretch)")
	hoursCSV := fs.String("hours", "", "comma-separated audio-hour scales to report (default 1,10,100 + largest run)")
	jsonOut := fs.Bool("json", false, "JSON output")
	if err := fs.Parse(args); err != nil {
		return 64
	}
	var hours []float64
	for _, p := range strings.Split(*hoursCSV, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		v, err := strconv.ParseFloat(p, 64)
		if err != nil {
			fmt.Fprintf(a.errOut, "noto bench scale: bad --hours value %q\n", p)
			return 64
		}
		hours = append(hours, v)
	}
	ctx := context.Background()
	client, close, code := a.connect(ctx)
	if code != 0 {
		return code
	}
	defer close()
	res, err := client.BenchScale(ctx, notoapi.BenchScaleRequest{
		TargetCostPerAudioHourUSD: *target,
		AudioHours:                hours,
	})
	if err != nil {
		return a.errExit(err)
	}
	if *jsonOut {
		return a.emitJSON(res)
	}
	a.printBenchScale(res)
	return 0
}

func (a *app) printBenchScale(res notoapi.BenchScaleResult) {
	fmt.Fprintf(a.out, "fixed overhead: $%.4f/run   marginal floor: $%.4f/audio-hr\n",
		res.FixedUSD, res.MarginalUSDPerAudioHour)
	fmt.Fprintf(a.out, "%-12s %12s %18s\n", "audio_hours", "cost_usd", "$/audio-hr")
	for _, p := range res.Points {
		fmt.Fprintf(a.out, "%-12.2f %12.4f %18.4f\n", p.AudioHours, p.CostUSD, p.CostPerAudioHourUSD)
	}
	if res.TargetReachableByScale {
		fmt.Fprintf(a.out, "target $%.4f/audio-hr reachable by scale at ~%.1f h of audio\n",
			res.TargetUSDPerAudioHour, res.HoursToReachTarget)
	} else {
		fmt.Fprintf(a.out, "target $%.4f/audio-hr NOT reachable by scale (marginal floor $%.4f) — needs a utilization/batching/work-reduction win\n",
			res.TargetUSDPerAudioHour, res.FloorUSDPerAudioHour)
	}
	if rd := res.Readiness; rd != nil {
		verdict := "READY to scale to full anchor"
		if !rd.Ready {
			verdict = "NOT ready to scale"
		}
		fmt.Fprintf(a.out, "scale-readiness: %s (busy %.1f%%/min %.0f%%, quality_ok=%v, 10h cost $%.4f)\n",
			verdict, rd.BusyPct, rd.MinBusyPct, rd.QualityWithinGuardrails, rd.CostPerAudioHourAt10hUSD)
		for _, reason := range rd.Reasons {
			fmt.Fprintf(a.out, "  blocker: %s\n", reason)
		}
	}
	for _, n := range res.Notes {
		fmt.Fprintf(a.out, "note: %s\n", n)
	}
}

func splitKnob(s string) (string, string, bool) {
	for i := 0; i < len(s); i++ {
		if s[i] == '=' {
			return s[:i], s[i+1:], true
		}
	}
	return "", "", false
}

func (a *app) runBenchPreflight(args []string) int {
	fs := flag.NewFlagSet("bench preflight", flag.ContinueOnError)
	suite := fs.String("suite", "", "suite id")
	tier := fs.String("tier", "gate", "budget tier")
	mode := fs.String("mode", "batch_queue", "operating mode")
	profile := fs.String("profile", "modal_cuda", "execution profile")
	budget := fs.Float64("budget-usd", 1.5, "budget cap USD")
	jsonOut := fs.Bool("json", false, "JSON output")
	if err := fs.Parse(args); err != nil {
		return 64
	}
	ctx := context.Background()
	client, close, code := a.connect(ctx)
	if code != 0 {
		return code
	}
	defer close()
	res, err := client.BenchPreflight(ctx, notoapi.BenchPreflightRequest{
		SuiteID:          *suite,
		Tier:             *tier,
		OperatingMode:    *mode,
		ExecutionProfile: *profile,
		BudgetUSDCap:     *budget,
		AgentID:          benchAgentID(""),
	})
	if err != nil {
		return a.errExit(err)
	}
	if *jsonOut {
		return a.emitJSON(res)
	}
	fmt.Fprintf(a.out, "suite: %s tier: %s blockers: %v\n", res.SuiteID, res.Tier, res.Blockers)
	return 0
}

func (a *app) runBenchCompare(args []string) int {
	fs := flag.NewFlagSet("bench compare", flag.ContinueOnError)
	baseline := fs.String("baseline", "", "baseline run id")
	candidate := fs.String("candidate", "", "candidate run id")
	jsonOut := fs.Bool("json", false, "JSON output")
	if err := fs.Parse(args); err != nil {
		return 64
	}
	if *baseline == "" || *candidate == "" {
		fmt.Fprintln(a.errOut, "noto bench compare --baseline <id> --candidate <id>")
		return 64
	}
	ctx := context.Background()
	client, close, code := a.connect(ctx)
	if code != 0 {
		return code
	}
	defer close()
	res, err := client.BenchCompare(ctx, notoapi.BenchCompareRequest{
		BaselineRunID:  *baseline,
		CandidateRunID: *candidate,
	})
	if err != nil {
		return a.errExit(err)
	}
	if *jsonOut {
		return a.emitJSON(res)
	}
	fmt.Fprintf(a.out, "decision: %s comparable: %v delta_pct: %.1f%%\n", res.Decision, res.Comparable, res.Cost.DeltaPct)
	if g := res.GPU; g != nil {
		fmt.Fprintf(a.out, "gpu idle/audio-hr: $%.4f → $%.4f (Δ $%.4f) busy %.1f%% → %.1f%%\n",
			g.BaselineIdleCostPerAudioHourUSD, g.CandidateIdleCostPerAudioHourUSD,
			g.DeltaIdleCostPerAudioHourUSD, g.BaselineBusyPct, g.CandidateBusyPct)
	}
	if s := res.Scale; s != nil {
		fmt.Fprintf(a.out, "marginal floor/audio-hr: $%.4f → $%.4f (Δ $%.4f) — %s\n",
			s.BaselineFloorUSDPerAudioHour, s.CandidateFloorUSDPerAudioHour, s.DeltaFloorUSDPerAudioHour,
			map[bool]string{true: "$0.01 reachable by scale", false: "needs a rate win for $0.01"}[s.TargetReachableByScale])
	}
	for _, ins := range res.AttributionInsights {
		fmt.Fprintf(a.out, "insight: %s\n", ins)
	}
	if len(res.GuardrailsFailed) > 0 {
		fmt.Fprintf(a.out, "failed: %v\n", res.GuardrailsFailed)
	}
	if len(res.GuardrailsSkipped) > 0 {
		fmt.Fprintf(a.out, "skipped: %v\n", res.GuardrailsSkipped)
	}
	return 0
}

func (a *app) runBenchDataset(args []string) int {
	if len(args) == 0 || args[0] != "list" {
		fmt.Fprintln(a.errOut, "noto bench dataset list [--json]")
		return 64
	}
	fs := flag.NewFlagSet("bench dataset list", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "JSON output")
	if err := fs.Parse(args[1:]); err != nil {
		return 64
	}
	ctx := context.Background()
	client, close, code := a.connect(ctx)
	if code != 0 {
		return code
	}
	defer close()
	res, err := client.BenchDatasetList(ctx)
	if err != nil {
		return a.errExit(err)
	}
	if *jsonOut {
		return a.emitJSON(res)
	}
	for _, s := range res.Suites {
		fmt.Fprintf(a.out, "%s (%s) %s\n", s.ID, s.ModalSuite, s.Description)
	}
	return 0
}

func (a *app) runBenchLedger(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(a.errOut, "noto bench ledger [winners|append]")
		return 64
	}
	switch args[0] {
	case "winners":
		return a.runBenchLedgerWinners(args[1:])
	case "append":
		return a.runBenchLedgerAppend(args[1:])
	default:
		fmt.Fprintf(a.errOut, "noto bench ledger: unknown %q\n", args[0])
		return 64
	}
}

func (a *app) runBenchLedgerWinners(args []string) int {
	fs := flag.NewFlagSet("bench ledger winners", flag.ContinueOnError)
	profile := fs.String("profile", "", "execution profile filter")
	mode := fs.String("mode", "", "operating mode filter")
	jsonOut := fs.Bool("json", false, "JSON output")
	if err := fs.Parse(args); err != nil {
		return 64
	}
	ctx := context.Background()
	client, close, code := a.connect(ctx)
	if code != 0 {
		return code
	}
	defer close()
	res, err := client.BenchLedgerWinners(ctx, notoapi.BenchLedgerWinnersOpts{
		Profile: *profile,
		Mode:    *mode,
	})
	if err != nil {
		return a.errExit(err)
	}
	if *jsonOut {
		return a.emitJSON(res)
	}
	for _, w := range res.Winners {
		fmt.Fprintf(a.out, "%s %s×%s $%.4f/hr\n", w.RunID, w.ExecutionProfile, w.OperatingMode, w.CostPerProcessedAudioHourUSD)
	}
	return 0
}

func (a *app) runBenchLedgerAppend(args []string) int {
	fs := flag.NewFlagSet("bench ledger append", flag.ContinueOnError)
	runID := fs.String("run", "", "run id")
	decision := fs.String("decision", "", "adopt|reject|retry|needs_confirm")
	baseline := fs.String("baseline", "", "baseline run id")
	hypothesis := fs.String("hypothesis", "", "hypothesis id")
	historical := fs.Bool("historical-unverified", false, "mark import as unverified")
	agentID := fs.String("agent-id", "", "agent id")
	jsonOut := fs.Bool("json", false, "JSON output")
	if err := fs.Parse(args); err != nil {
		return 64
	}
	if *runID == "" || *decision == "" {
		fmt.Fprintln(a.errOut, "noto bench ledger append --run <id> --decision <decision>")
		return 64
	}
	ctx := context.Background()
	client, close, code := a.connect(ctx)
	if code != 0 {
		return code
	}
	defer close()
	res, err := client.BenchLedgerAppend(ctx, notoapi.BenchLedgerAppendRequest{
		RunID:                *runID,
		BaselineRunID:        *baseline,
		Decision:             *decision,
		HypothesisID:         *hypothesis,
		AgentID:              benchAgentID(*agentID),
		HistoricalUnverified: *historical,
	})
	if err != nil {
		return a.errExit(err)
	}
	if *jsonOut {
		return a.emitJSON(res)
	}
	fmt.Fprintf(a.out, "appended %s decision=%s\n", res.RunID, res.Decision)
	return 0
}
