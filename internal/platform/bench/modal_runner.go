package bench

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	corebench "github.com/lukasstrickler/noto/internal/core/bench"
	"github.com/lukasstrickler/noto/internal/platform/config"
)

// RunRequest is input for a benchmark execution.
type RunRequest struct {
	Manifest        corebench.RunManifest
	IntegrationOnly bool
	FixtureRoot     string // test override for golden_pipeline ingest
	GPU             string
}

// RunResponse is bench_run result metadata.
type RunResponse struct {
	RunID                        string
	ArtifactDir                  string
	EstimatedCostUSD             float64
	CostPerProcessedAudioHourUSD float64
	UnattributedPct              float64
	TraceValid                   bool
	GPUBusyPct                   float64
	GPUIdleCostPerAudioHourUSD   float64
	Manifest                     corebench.RunManifest
}

// IngestGolden builds trace_summary from modal summary + hyp + pyannote stderr (A6.0).
func IngestGolden(summary ModalSummary, hyp MeetingHyp, stderr string, stageMap PyannoteStageMap) corebench.TraceSummary {
	timing := ParsePyannoteStderr(stderr)
	return BuildTraceSummary(summary, []MeetingHyp{hyp}, timing, stageMap)
}

// PersistRun writes compute_audit, trace_summary, metrics for a completed ingest.
func (s *Store) PersistRun(runID string, audit map[string]any, trace corebench.TraceSummary, metrics corebench.MetricsFile) error {
	if err := s.WriteJSON(runID, "compute_audit.json", audit); err != nil {
		return err
	}
	if err := s.WriteJSON(runID, "trace_summary.json", trace); err != nil {
		return err
	}
	return s.WriteJSON(runID, "metrics.json", metrics)
}

// LoadModalSummary reads summary.json from a path.
func LoadModalSummary(path string) (ModalSummary, error) {
	var s ModalSummary
	b, err := os.ReadFile(path)
	if err != nil {
		return s, err
	}
	err = json.Unmarshal(b, &s)
	return s, err
}

// ModalRunner wraps scripts/modal_benchmark.py subprocess execution.
type ModalRunner struct {
	Store    *Store
	Script   string
	RepoRoot string
	Python   string
}

func NewModalRunner(store *Store, repoRoot string) *ModalRunner {
	return &ModalRunner{
		Store:    store,
		RepoRoot: repoRoot,
		Script:   filepath.Join(repoRoot, "scripts", "modal_benchmark.py"),
		Python:   PythonForBench(repoRoot),
	}
}

// IngestFromFiles loads golden pipeline inputs from testdata paths (unit tests).
func IngestFromFiles(summaryPath, hypPath, stderrPath, stageMapPath string) (ModalSummary, MeetingHyp, string, PyannoteStageMap, error) {
	var summary ModalSummary
	b, err := os.ReadFile(summaryPath)
	if err != nil {
		return summary, MeetingHyp{}, "", nil, err
	}
	if err := json.Unmarshal(b, &summary); err != nil {
		return summary, MeetingHyp{}, "", nil, err
	}
	var hyp MeetingHyp
	b, err = os.ReadFile(hypPath)
	if err != nil {
		return summary, hyp, "", nil, err
	}
	if err := json.Unmarshal(b, &hyp); err != nil {
		return summary, hyp, "", nil, err
	}
	stderrBytes, err := os.ReadFile(stderrPath)
	if err != nil {
		return summary, hyp, "", nil, err
	}
	mapBytes, err := os.ReadFile(stageMapPath)
	if err != nil {
		return summary, hyp, "", nil, err
	}
	stageMap, err := LoadPyannoteStageMap(mapBytes)
	return summary, hyp, string(stderrBytes), stageMap, err
}

// Run executes a benchmark (integration fixture or Modal subprocess).
func (m *ModalRunner) Run(ctx context.Context, req RunRequest) (RunResponse, error) {
	if req.Manifest.RunID == "" {
		req.Manifest.RunID = newRunID(req.Manifest)
	}
	manifest := req.Manifest
	manifest.BenchStack = "bench_cuda"
	if manifest.Comparability.TraceMode == "" {
		manifest.Comparability.TraceMode = "summary"
	}

	var (
		summary  ModalSummary
		hyps     []MeetingHyp
		pyannote string
		stageMap PyannoteStageMap
		rawDir   string
		err      error
	)

	if req.IntegrationOnly {
		summary, hyps, pyannote, stageMap, err = m.loadGoldenFixture(req)
	} else {
		rawDir, summary, hyps, pyannote, stageMap, err = m.runSubprocess(ctx, req)
	}
	if err != nil {
		return RunResponse{}, err
	}

	if summary.RunID != "" {
		manifest.RunID = summary.RunID
	} else {
		summary.RunID = manifest.RunID
	}

	trace := BuildTraceSummary(summary, hyps, ParsePyannoteStderr(pyannote), stageMap)
	audit, err := MapSummaryToAudit(summary, trace, manifest.ExecutionProfile, manifest.OperatingMode)
	if err != nil {
		return RunResponse{}, err
	}
	metrics, ok := MetricsFromSummaryKPIs(manifest.RunID, summary)
	if len(hyps) > 0 || !ok {
		var err error
		metrics, err = ScoreMeetingHyps(manifest.RunID, hyps, ScoreOptionsForRepo(m.RepoRoot, ResolveSuite(manifest.SuiteID)))
		if err != nil {
			return RunResponse{}, err
		}
	}

	if err := m.Store.WriteJSON(manifest.RunID, "manifest.json", manifest); err != nil {
		return RunResponse{}, err
	}
	if err := m.Store.PersistRun(manifest.RunID, audit, trace, metrics); err != nil {
		return RunResponse{}, err
	}
	// Score word-confidence calibration (B6) when the run emitted confidence.
	// Best-effort: the metrics + trace are the run's contract; a calibration
	// scoring failure must never fail the run, and a no-confidence run simply
	// leaves no calibration.json.
	if words, cerr := scoreCalibration(hyps, ScoreOptionsForRepo(m.RepoRoot, ResolveSuite(manifest.SuiteID))); cerr == nil && len(words) > 0 {
		_ = m.Store.WriteJSON(manifest.RunID, "calibration.json", corebench.BuildCalibrationReport(words))
	}
	if rawDir != "" {
		_ = m.copyOptionalRaw(manifest.RunID, rawDir)
	}

	cph := summary.CostPerAudioHourUSD
	if cph == 0 && summary.ProcessedAudioHours > 0 {
		cph = summary.EstimatedCostUSD / summary.ProcessedAudioHours
	}

	resp := RunResponse{
		RunID:                        manifest.RunID,
		ArtifactDir:                  m.Store.RunDir(manifest.RunID),
		EstimatedCostUSD:             summary.EstimatedCostUSD,
		CostPerProcessedAudioHourUSD: cph,
		UnattributedPct:              trace.CostWaterfall.UnattributedPct,
		TraceValid:                   trace.CostWaterfall.UnattributedPct < 2,
		Manifest:                     manifest,
	}
	if trace.GPU != nil {
		resp.GPUBusyPct = trace.GPU.BusyPct
		resp.GPUIdleCostPerAudioHourUSD = trace.GPU.IdleCostPerAudioHourUSD
	}
	return resp, nil
}

func (m *ModalRunner) loadGoldenFixture(req RunRequest) (ModalSummary, []MeetingHyp, string, PyannoteStageMap, error) {
	root := req.FixtureRoot
	if root == "" {
		root = corebench.FixtureRoot()
	}
	gp := filepath.Join(root, "golden_pipeline")
	summary, hyp, stderr, stageMap, err := IngestFromFiles(
		filepath.Join(gp, "summary_snippet.json"),
		filepath.Join(gp, "hyp_es2002a.json"),
		filepath.Join(gp, "pyannote_stderr.txt"),
		filepath.Join(root, "pyannote_stage_map_v1.json"),
	)
	if err != nil {
		return summary, nil, "", nil, err
	}
	summary.RunID = req.Manifest.RunID
	return summary, []MeetingHyp{hyp}, stderr, stageMap, nil
}

func (m *ModalRunner) runSubprocess(ctx context.Context, req RunRequest) (string, ModalSummary, []MeetingHyp, string, PyannoteStageMap, error) {
	if _, err := os.Stat(m.Script); err != nil {
		return "", ModalSummary{}, nil, "", nil, fmt.Errorf("modal benchmark script: %w", err)
	}
	outDir, err := os.MkdirTemp("", "noto-bench-out-")
	if err != nil {
		return "", ModalSummary{}, nil, "", nil, err
	}

	gpu := req.GPU
	if gpu == "" {
		gpu = config.DefaultModalGPU
	}
	args := ModalBenchmarkArgs(m.Script, outDir, gpu, req.Manifest)
	cmd := exec.CommandContext(ctx, m.Python, args...)
	cmd.Dir = m.RepoRoot
	cmd.Env = append(os.Environ(), knobEnv(req.Manifest.Knobs)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", ModalSummary{}, nil, "", nil, fmt.Errorf("modal benchmark: %w: stderr=%s stdout_tail=%s raw_out=%s",
			err,
			strings.TrimSpace(stderr.String()),
			tailString(stdout.String(), 2000),
			outDir,
		)
	}

	stdoutSummary, hasStdout := summaryFromStdout(stdout.Bytes())
	rawRunDir, summary, hyps, pyannoteLine, stageMap, err := m.ingestModalOutDir(outDir, stdoutSummary, hasStdout)
	if err != nil {
		return "", ModalSummary{}, nil, "", nil, err
	}
	return rawRunDir, summary, hyps, pyannoteLine, stageMap, nil
}

func tailString(s string, limit int) string {
	s = strings.TrimSpace(s)
	if len(s) <= limit {
		return s
	}
	return s[len(s)-limit:]
}

func ModalBenchmarkArgs(script, outDir, gpu string, manifest corebench.RunManifest) []string {
	suite := ResolveSuite(manifest.SuiteID)
	profile := ModalProfile(manifest.OperatingMode)
	jobs := TargetConcurrency(manifest.OperatingMode, manifest.Knobs)
	args := []string{
		script, "run",
		"--suite", suite.ModalSuite,
		"--profile", profile,
		"--gpu", gpu,
		"--jobs", fmt.Sprintf("%d", jobs),
		"--out", outDir,
	}
	if suite.Quick {
		args = append(args, "--quick")
	}
	if suite.Hours > 0 {
		args = append(args, "--hours", fmt.Sprintf("%g", suite.Hours))
	}
	if bw, ok := manifest.Knobs["batch_wait_ms"]; ok && bw != "" {
		args = append(args, "--batch-wait-ms", bw)
	}
	return args
}

// knobLauncherEnv maps the short, plan-documented optimization knob names
// (§0.8 `--knob vad=on`) to the launcher-side env var modal_benchmark.py reads
// from KNOB_FORWARDS (the `local` column). The Python forwards each of these
// into the Modal container under its `container` name (e.g. BENCH_VAD → NOTO_VAD,
// which scripts/vad_trim.py consumes). Without this, a bare `vad` knob would map
// to NOTO_VAD on the launcher, which the Python ignores — the trim never runs.
var knobLauncherEnv = map[string]string{
	"vad":            "BENCH_VAD",
	"vad_pad":        "BENCH_VAD_PAD",
	"vad_min_gap":    "BENCH_VAD_MIN_GAP",
	"vad_threshold":  "BENCH_VAD_THRESHOLD",
	"nemo_precision": "BENCH_NEMO_PRECISION",
	"nemo_batch":     "BENCH_NEMO_BATCH",
	"emb_compile":    "BENCH_PYANNOTE_EMB_COMPILE",
	// emb_batch/seg_batch raise pyannote's embedding/segmentation batch sizes
	// (configure_batching in pyannote_diar_server.py) so the GPU processes more
	// speaker segments per forward pass — the H6 rate-win lever on diar embedding,
	// the ~90%-of-cost stage. The L40S has ample VRAM headroom (~19/48 GB used at
	// jobs=10) to grow the batch.
	"emb_batch":    "BENCH_PYANNOTE_EMB_BATCH",
	"seg_batch":    "BENCH_PYANNOTE_SEG_BATCH",
	"diar_workers": "BENCH_DIAR_WORKERS",
	"diar_streams": "BENCH_DIAR_STREAMS",
	"cuda_mps":     "BENCH_CUDA_MPS",
	// confidence turns on NeMo word confidence (NOTO_PARAKEET_CONFIDENCE) so the
	// run's hyps carry per-word P(correct) — the input B6 calibration and B7 repair
	// candidate selection both need.
	"confidence": "BENCH_PARAKEET_CONFIDENCE",
	// decode selects the NeMo decode SEARCH (greedy default | beam | maes | …) and
	// beam_size its width — the §10.5 same-model alternate decode that produces a
	// genuinely different hypothesis for the B7 repair re-decode (greedy across
	// precision is deterministic on content, so an alternate STRATEGY, not fp32,
	// is the lever that can actually fix low-confidence spans).
	"decode":    "BENCH_PARAKEET_DECODE",
	"beam_size": "BENCH_PARAKEET_BEAM_SIZE",
	// perturb re-decodes the SAME model on ALTERED audio (speed:0.9 | speed:1.1 |
	// noise:0.005) — a test-time augmentation that produces a genuinely different
	// hypothesis WITHOUT a weaker model, the repair lever when same-model decode
	// config is byte-identical. speed warps time (timestamps rescaled back).
	"perturb": "BENCH_PARAKEET_PERTURB",
	// stt_model swaps the NeMo STT weights (NOTO_PARAKEET_MODEL) — the §10.5
	// alternate-ASR repair method: re-decode low-confidence spans with a different
	// (e.g. larger) parakeet, the genuine-quality alternate when same-model beam
	// isn't enough. Same word-timestamp output shape, so the repair loop is unchanged.
	"stt_model": "BENCH_PARAKEET_MODEL",
}

func knobEnv(knobs map[string]string) []string {
	if len(knobs) == 0 {
		return nil
	}
	env := make([]string, 0, len(knobs))
	for k, v := range knobs {
		if mapped, ok := knobLauncherEnv[strings.ToLower(k)]; ok {
			env = append(env, mapped+"="+v)
			continue
		}
		key := strings.ToUpper(k)
		if !strings.HasPrefix(key, "NOTO_") && !strings.HasPrefix(key, "BENCH_") {
			key = "NOTO_" + key
		}
		env = append(env, key+"="+v)
	}
	return env
}

var summaryJSONRe = regexp.MustCompile(`(?s)\{.*"run_id".*\}`)

func summaryFromStdout(stdout []byte) (ModalSummary, bool) {
	trim := strings.TrimSpace(string(stdout))
	if trim == "" {
		return ModalSummary{}, false
	}
	// modal_benchmark prints summary JSON as last stdout block
	start := strings.LastIndex(trim, "{")
	if start < 0 {
		return ModalSummary{}, false
	}
	block := trim[start:]
	var s ModalSummary
	if err := json.Unmarshal([]byte(block), &s); err != nil {
		if m := summaryJSONRe.FindString(trim); m != "" {
			if err2 := json.Unmarshal([]byte(m), &s); err2 != nil {
				return ModalSummary{}, false
			}
			return s, true
		}
		return ModalSummary{}, false
	}
	return s, true
}

func (m *ModalRunner) ingestModalOutDir(outDir string, stdoutSummary ModalSummary, hasStdout bool) (string, ModalSummary, []MeetingHyp, string, PyannoteStageMap, error) {
	stageMap, err := LoadPyannoteStageMap(nil)
	if err != nil {
		return "", ModalSummary{}, nil, "", nil, err
	}
	if b, err := os.ReadFile(filepath.Join(corebench.FixtureRoot(), "pyannote_stage_map_v1.json")); err == nil {
		stageMap, _ = LoadPyannoteStageMap(b)
	}

	var runDir string
	if hasStdout && stdoutSummary.RunID != "" {
		runDir = filepath.Join(outDir, stdoutSummary.RunID)
	} else if entries, err := os.ReadDir(outDir); err == nil {
		for _, e := range entries {
			if e.IsDir() && e.Name() != "latest" {
				runDir = filepath.Join(outDir, e.Name())
				break
			}
		}
	}
	if runDir == "" {
		return "", ModalSummary{}, nil, "", nil, fmt.Errorf("no run directory under %s", outDir)
	}

	summaryPath := filepath.Join(runDir, "summary.json")
	summary, err := LoadModalSummary(summaryPath)
	if err != nil && hasStdout {
		summary = stdoutSummary
	} else if err != nil {
		return "", ModalSummary{}, nil, "", nil, err
	}

	hyps, err := loadMeetingHyps(filepath.Join(runDir, "hyps"))
	if err != nil {
		return "", ModalSummary{}, nil, "", nil, err
	}
	setupLog, _ := os.ReadFile(filepath.Join(runDir, "setup.log"))
	pyannote := extractPyannoteStderr(string(setupLog))
	return runDir, summary, hyps, pyannote, stageMap, nil
}

func loadMeetingHyps(dir string) ([]MeetingHyp, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var hyps []MeetingHyp
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue // skip an unreadable hyp rather than voiding the whole capture
		}
		var h MeetingHyp
		if err := json.Unmarshal(b, &h); err != nil {
			// Hyps are written incrementally (non-atomically) so a crash mid-run
			// keeps finished meetings; a truncated/corrupt file is exactly that
			// case. Skip it and score the rest, matching the Python producer's
			// per-file try/except — one bad file must not void a whole capture.
			continue
		}
		if h.MeetingID == "" {
			if h.ID != "" {
				h.MeetingID = h.ID
			} else {
				h.MeetingID = strings.TrimSuffix(e.Name(), ".json")
			}
		}
		hyps = append(hyps, h)
	}
	return hyps, nil
}

// stagesMSLine isolates the one timing line from a multi-line setup.log so the
// per-line parsers don't sweep tokens from unrelated lines. It matches both the
// live bracketed stage group (`[segmentation=… embeddings=…]`) and the legacy
// `stages_ms …` form — the live server emits the former, so anchoring only on
// `stages_ms` returned the whole log (a no-op isolation) on every real run.
var stagesMSLine = regexp.MustCompile(`[^\n]*(?:stages_ms|\[[^\]\n]*=[^\]\n]*\])[^\n]*`)

func extractPyannoteStderr(setupLog string) string {
	if m := stagesMSLine.FindString(setupLog); m != "" {
		return m
	}
	return setupLog
}

func (m *ModalRunner) copyOptionalRaw(runID, srcDir string) error {
	for _, name := range []string{"summary.json", "setup.log", "raw.jsonl"} {
		src := filepath.Join(srcDir, name)
		if _, err := os.Stat(src); err != nil {
			continue
		}
		b, err := os.ReadFile(src)
		if err != nil {
			continue
		}
		_ = os.WriteFile(filepath.Join(m.Store.RunDir(runID), name), b, 0o600)
	}
	_ = copyJSONDir(filepath.Join(srcDir, "hyps"), filepath.Join(m.Store.RunDir(runID), "hyps"))
	return nil
}

func copyJSONDir(srcDir, dstDir string) error {
	entries, err := os.ReadDir(srcDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dstDir, 0o700); err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(srcDir, entry.Name()))
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dstDir, entry.Name()), b, 0o600); err != nil {
			return err
		}
	}
	return nil
}

func newRunID(m corebench.RunManifest) string {
	ts := time.Now().UTC().Format("20060102T150405")
	slug := strings.SplitN(m.SuiteID, "@", 2)[0]
	slug = strings.ReplaceAll(slug, "_", "")
	return fmt.Sprintf("run_%s_%s_%s", ts, slug, strings.TrimPrefix(m.Tier, "baseline_"))
}

// RunSubprocess exposes subprocess for tests that mock at a higher layer.
func (m *ModalRunner) RunSubprocess(ctx context.Context, args []string) error {
	if len(args) == 0 {
		args = []string{"run", "--suite", "synthetic", "--quick"}
	}
	cmd := exec.CommandContext(ctx, m.Python, append([]string{m.Script}, args...)...)
	cmd.Dir = m.RepoRoot
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w: %s", err, stderr.String())
	}
	return nil
}
