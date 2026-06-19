package cli

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lukasstrickler/noto/internal/app/host"
	"github.com/lukasstrickler/noto/internal/platform/compute"
	"github.com/lukasstrickler/noto/internal/platform/models"
	"github.com/lukasstrickler/noto/internal/platform/providers/speaker"
	"github.com/lukasstrickler/noto/internal/transport/notoapi"
	"github.com/lukasstrickler/noto/internal/ui/tui"
)

// This file holds the runtime/control verbs: launching the TUI, running
// the serve daemon, recording, importing, inspecting jobs/status, and the
// dev/seed helpers. These either start a server or mutate backend state.

// --- TUI ---

func (a *app) runTUI(_ []string) int {
	ctx, cancel := signalContext()
	defer cancel()
	client, closer, code := a.connect(ctx)
	if code != 0 {
		return code
	}
	defer closer()
	if err := tui.Run(ctx, client); err != nil {
		fmt.Fprintf(a.errOut, "noto tui: %v\n", err)
		return 1
	}
	return 0
}

// --- Serve ---

func (a *app) runServe(args []string) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	listen := fs.String("listen", "unix:", "listen address: 'unix:<path>' or 'tcp:<host:port>'")
	tokenFile := fs.String("token-file", "", "path to read/write the bearer token for TCP mode")
	if err := fs.Parse(args); err != nil {
		return 64
	}
	network, addr, err := parseListen(*listen)
	if err != nil {
		fmt.Fprintf(a.errOut, "noto serve: %v\n", err)
		return 64
	}
	token := ""
	if network == "tcp" {
		if *tokenFile != "" {
			if data, err := os.ReadFile(*tokenFile); err == nil && len(data) > 0 {
				token = strings.TrimSpace(string(data))
			}
		}
		if token == "" {
			token = randomToken()
			if *tokenFile != "" {
				if err := os.WriteFile(*tokenFile, []byte(token), 0o600); err != nil {
					fmt.Fprintf(a.errOut, "noto serve: write token file: %v\n", err)
					return 1
				}
			}
		}
	}

	ctx, cancel := signalContext()
	defer cancel()

	host, err := host.Start(ctx, host.Options{
		Network:    network,
		Address:    addr,
		Token:      token,
		ServerOnly: true,
		Version:    "0.1.0",
	})
	if err != nil {
		fmt.Fprintf(a.errOut, "noto serve: %v\n", err)
		return 1
	}
	defer host.Close()

	switch network {
	case "unix":
		fmt.Fprintf(a.out, "noto serve  listening on unix:%s\n", host.Addr())
	case "tcp":
		fmt.Fprintf(a.out, "noto serve  listening on tcp:%s\n", host.Addr())
		fmt.Fprintf(a.out, "noto serve  bearer token: %s\n", host.Token())
	}
	fmt.Fprintln(a.out, "noto serve  press Ctrl+C to stop")
	<-ctx.Done()
	return 0
}

func parseListen(s string) (string, string, error) {
	if !strings.Contains(s, ":") {
		return "", "", errors.New("listen must be 'unix:<path>' or 'tcp:<host:port>'")
	}
	parts := strings.SplitN(s, ":", 2)
	network := parts[0]
	addr := parts[1]
	switch network {
	case "unix":
		return "unix", addr, nil
	case "tcp":
		return "tcp", addr, nil
	default:
		return "", "", fmt.Errorf("unknown network %q", network)
	}
}

// --- status / providers / verify / record / stop / jobs / ping ---

func (a *app) runStatus(args []string) int {
	ctx, cancel := defaultCtx()
	defer cancel()
	client, closer, code := a.connect(ctx)
	if code != 0 {
		return code
	}
	defer closer()
	h, err := client.Health(ctx)
	if err != nil {
		return a.errExit(err)
	}
	rec, _ := client.GetRecording(ctx)
	jobs, _ := client.ListJobs(ctx, notoapi.ListJobsOpts{Limit: 10})
	sys, _ := client.GetSystem(ctx)
	out := map[string]any{
		"health":    h,
		"system":    sys,
		"recording": rec,
		"jobs":      jobs,
	}
	return a.emitJSON(out)
}

func (a *app) runProviders(args []string) int {
	if len(args) == 0 || strings.HasPrefix(args[0], "--") {
		args = append([]string{"list"}, args...)
	}
	ctx, cancel := defaultCtx()
	defer cancel()
	client, closer, code := a.connect(ctx)
	if code != 0 {
		return code
	}
	defer closer()
	switch args[0] {
	case "list":
		list, err := client.ListProviders(ctx)
		if err != nil {
			return a.errExit(err)
		}
		if hasFlag(args, "--json") {
			return a.emitJSON(list)
		}
		for _, p := range list {
			key := "no-key"
			if p.HasKey {
				key = "ok (" + p.KeySource + ")"
			}
			fmt.Fprintf(a.out, "%-12s %-6s  %s\n", p.ID, p.Kind, key)
		}
		return 0
	case "key-set":
		if len(args) < 3 {
			fmt.Fprintln(a.errOut, "noto providers key-set <provider> <value>")
			return 64
		}
		if err := client.SetProviderKey(ctx, args[1], args[2]); err != nil {
			return a.errExit(err)
		}
		fmt.Fprintln(a.out, "ok")
		return 0
	case "key-remove":
		if len(args) < 2 {
			fmt.Fprintln(a.errOut, "noto providers key-remove <provider>")
			return 64
		}
		if err := client.DeleteProviderKey(ctx, args[1]); err != nil {
			return a.errExit(err)
		}
		fmt.Fprintln(a.out, "ok")
		return 0
	case "test":
		if len(args) < 2 {
			fmt.Fprintln(a.errOut, "noto providers test <provider>")
			return 64
		}
		r, err := client.TestProvider(ctx, args[1])
		if err != nil {
			return a.errExit(err)
		}
		return a.emitJSON(r)
	case "active-speech":
		if len(args) < 2 {
			fmt.Fprintln(a.errOut, "noto providers active-speech <provider>")
			return 64
		}
		if err := client.SetActiveSpeech(ctx, args[1]); err != nil {
			return a.errExit(err)
		}
		fmt.Fprintln(a.out, "ok")
		return 0
	case "active-llm":
		if len(args) < 2 {
			fmt.Fprintln(a.errOut, "noto providers active-llm <model>")
			return 64
		}
		if err := client.SetActiveLLMModel(ctx, args[1]); err != nil {
			return a.errExit(err)
		}
		fmt.Fprintln(a.out, "ok")
		return 0
	default:
		fmt.Fprintf(a.errOut, "noto providers: unknown subcommand %q\n", args[0])
		return 64
	}
}

func (a *app) runVerify(args []string) int {
	ctx, cancel := defaultCtx()
	defer cancel()
	client, closer, code := a.connect(ctx)
	if code != 0 {
		return code
	}
	defer closer()
	j, err := client.VerifyStorage(ctx)
	if err != nil {
		return a.errExit(err)
	}
	return a.emitJSON(j)
}

func (a *app) runRecord(args []string) int {
	fs := flag.NewFlagSet("record", flag.ContinueOnError)
	title := fs.String("title", "Untitled", "title for the recording")
	if err := fs.Parse(args); err != nil {
		return 64
	}
	ctx, cancel := defaultCtx()
	defer cancel()
	client, closer, code := a.connect(ctx)
	if code != 0 {
		return code
	}
	defer closer()
	res, err := client.StartRecording(ctx, notoapi.StartRecordingOpts{
		Title:     *title,
		Sources:   []string{"microphone", "system_audio"},
		AfterStop: notoapi.AfterStop{Ingest: true, Transcribe: true, Summarize: true, Index: true},
	})
	if err != nil {
		return a.errExit(err)
	}
	return a.emitJSON(res)
}

func (a *app) runImportAudio(args []string) int {
	fs := flag.NewFlagSet("import-audio", flag.ContinueOnError)
	title := fs.String("title", "", "title for the meeting (defaults to filename)")
	asJSON := fs.Bool("json", false, "emit JSON")
	wait := fs.Bool("wait", false, "block until the pipeline job finishes")
	// Split positional arg from flag args so the path can appear in
	// any position relative to the flags.
	srcPath, rest := pickPositional(args)
	if srcPath == "" {
		fmt.Fprintln(a.errOut, "noto import-audio <path> [--title \"...\"] [--wait] [--json]")
		return 64
	}
	if err := fs.Parse(rest); err != nil {
		return 64
	}
	abs, err := filepath.Abs(srcPath)
	if err != nil {
		return a.errExit(err)
	}
	ctx, cancel := defaultCtx()
	defer cancel()
	client, closer, code := a.connect(ctx)
	if code != 0 {
		return code
	}
	defer closer()
	res, err := client.ImportAudio(ctx, notoapi.ImportAudioOpts{Path: abs, Title: *title})
	if err != nil {
		return a.errExit(err)
	}
	if *wait {
		jobID := res.Job.ID
		fmt.Fprintf(a.errOut, "imported meeting %s, pipeline job %s\n", res.Meeting.ID, jobID)
		// Poll until terminal.
		ctxW, cancelW := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancelW()
		for {
			job, err := client.GetJob(ctxW, jobID)
			if err != nil {
				return a.errExit(err)
			}
			switch job.Status {
			case notoapi.JobSucceeded:
				if *asJSON {
					return a.emitJSON(job)
				}
				fmt.Fprintln(a.out, "pipeline succeeded:", res.Meeting.ID)
				return 0
			case notoapi.JobFailed, notoapi.JobCanceled, notoapi.JobInterrupted:
				return a.errExit(fmt.Errorf("pipeline %s: %s", job.Status, job.Error))
			}
			time.Sleep(500 * time.Millisecond)
		}
	}
	if *asJSON {
		return a.emitJSON(res)
	}
	fmt.Fprintf(a.out, "imported meeting %s (job %s)\n", res.Meeting.ID, res.Job.ID)
	return 0
}

// runSpeakerModel downloads or reports the local voiceprint model. The model, the
// onnxruntime shared library, and a static ffmpeg are stored in the data dir (never the
// repo); once present, the pipeline uses the in-process ECAPA provider automatically.
func (a *app) runSpeakerModel(args []string) int {
	sub := "status"
	if len(args) > 0 {
		sub = args[0]
	}
	ctx, cancel := defaultCtx()
	defer cancel()
	client, closer, code := a.connect(ctx)
	if code != 0 {
		return code
	}
	defer closer()
	paths, err := client.GetPaths(ctx)
	if err != nil {
		return a.errExit(err)
	}
	cfgDir := paths.ConfigDir
	p := speaker.ResolvePaths(cfgDir)
	switch sub {
	case "download":
		log := func(format string, args ...any) {
			fmt.Fprintf(a.errOut, "  "+format+"\n", args...)
		}
		fmt.Fprintf(a.errOut, "installing voiceprint assets into %s\n", p.Home)
		if err := speaker.Download(cfgDir, log); err != nil {
			return a.errExit(err)
		}
		if speaker.Available(cfgDir) {
			fmt.Fprintln(a.out, "speaker model ready:", p.Model)
			return 0
		}
		return a.errExit(fmt.Errorf("download finished but model not available"))
	case "status":
		fmt.Fprintf(a.out, "data dir : %s\n", p.Home)
		fmt.Fprintf(a.out, "model    : %s [%s]\n", p.Model, presentMark(p.Model))
		fmt.Fprintf(a.out, "onnxrt   : %s [%s]\n", p.Lib, presentMark(p.Lib))
		fmt.Fprintf(a.out, "ffmpeg   : %s\n", p.FfmpegPath())
		if speaker.Available(cfgDir) {
			fmt.Fprintln(a.out, "status   : ready (pipeline will use in-process ECAPA)")
		} else {
			fmt.Fprintln(a.out, "status   : not installed (run: noto speaker-model download)")
		}
		return 0
	default:
		fmt.Fprintln(a.errOut, "noto speaker-model [status|download]")
		return 64
	}
}

func presentMark(path string) string {
	if _, err := os.Stat(path); err == nil {
		return "installed"
	}
	return "missing"
}

// runModels manages the general local model store (STT + diarization weights
// and the onnxruntime/sherpa runtime). It fetches the variant matching the
// host's detected accelerator + tier. The speaker-embedding (ECAPA) model is
// still managed by `noto speaker-model` until the runtimes are unified.
func (a *app) runModels(args []string) int {
	sub := "status"
	if len(args) > 0 {
		sub = args[0]
	}
	ctx, cancel := defaultCtx()
	defer cancel()
	client, closer, code := a.connect(ctx)
	if code != 0 {
		return code
	}
	defer closer()
	paths, err := client.GetPaths(ctx)
	if err != nil {
		return a.errExit(err)
	}
	mgr := models.New(paths.ConfigDir)
	// Detect the accelerator/tier this machine would run. In local mode this is
	// the same backend the pipeline uses; in remote mode /v1/system is the
	// authoritative source (Phase 5) — this is a local approximation.
	plan := compute.DetectFromEnv(nil, nil)
	man := mgr.Manifest()

	switch sub {
	case "status":
		fmt.Fprintf(a.out, "backend  : %s (tier %s)\n", plan.Backend, plan.Tier)
		if lib, ok := mgr.RuntimeLib(plan.Backend); ok {
			fmt.Fprintf(a.out, "runtime  : %s [installed]\n", lib)
		} else {
			fmt.Fprintf(a.out, "runtime  : onnxruntime [missing] (run: noto models download runtime)\n")
		}
		fmt.Fprintf(a.out, "ffmpeg   : %s\n", mgr.FfmpegPath())
		for _, mdl := range man.Models {
			if mdl.Role == models.RoleEmbed {
				continue // managed by `noto speaker-model`
			}
			mark := "missing"
			if mgr.Available(mdl.ID, plan.Backend, plan.Tier) {
				mark = "installed"
			}
			fmt.Fprintf(a.out, "%-8s : %s [%s] (license %s)\n", string(mdl.Role), mdl.ID, mark, mdl.License)
		}
		fmt.Fprintln(a.out, "note     : voiceprint/ECAPA model is managed by `noto speaker-model`")
		return 0
	case "download":
		log := func(format string, args ...any) { fmt.Fprintf(a.errOut, "  "+format+"\n", args...) }
		mgr.OnProgress(func(asset string, downloaded, total int64) {
			if total > 0 {
				fmt.Fprintf(a.errOut, "\r  %s %d/%d MB ", asset, downloaded>>20, total>>20)
			}
		})
		if len(args) < 2 {
			fmt.Fprintln(a.errOut, "noto models download <model-id|runtime>")
			fmt.Fprintln(a.errOut, "available models:")
			for _, mdl := range man.Models {
				if mdl.Role == models.RoleEmbed {
					continue
				}
				fmt.Fprintf(a.errOut, "  %s (%s)\n", mdl.ID, mdl.Role)
			}
			return 64
		}
		id := args[1]
		if id == "runtime" {
			if err := mgr.EnsureRuntime(ctx, plan.Backend, log); err != nil {
				return a.errExit(err)
			}
			fmt.Fprintln(a.out, "runtime ready")
			return 0
		}
		if err := mgr.Download(ctx, id, plan.Backend, plan.Tier, log); err != nil {
			return a.errExit(err)
		}
		fmt.Fprintf(a.out, "\n%s ready (%s/%s)\n", id, plan.Backend, plan.Tier)
		return 0
	default:
		fmt.Fprintln(a.errOut, "noto models [status|download <id>]")
		return 64
	}
}

func (a *app) runModal(args []string) int {
	sub := "status"
	if len(args) > 0 {
		sub = args[0]
	}
	ctx, cancel := defaultCtx()
	defer cancel()
	client, closer, code := a.connect(ctx)
	if code != 0 {
		return code
	}
	defer closer()

	switch sub {
	case "status":
		st, err := client.GetModalComputeStatus(ctx)
		if err != nil {
			return a.errExit(err)
		}
		if hasFlag(args, "--json") {
			return a.emitJSON(st)
		}
		fmt.Fprintf(a.out, "provider : %s\n", st.Provider)
		fmt.Fprintf(a.out, "ready    : %v\n", st.Ready)
		fmt.Fprintf(a.out, "endpoint : %s\n", emptyDash(st.EndpointURL))
		fmt.Fprintf(a.out, "gpu      : %s\n", emptyDash(st.GPU))
		fmt.Fprintf(a.out, "version  : %s", emptyDash(st.DeploymentVersion))
		if st.NeedsUpdate {
			fmt.Fprintf(a.out, " (update available: %s)", st.DesiredVersion)
		}
		fmt.Fprintln(a.out)
		fmt.Fprintf(a.out, "user xfer : %s", emptyDash(st.UserTransferMode))
		if st.TemporaryThresholdMB > 0 {
			fmt.Fprintf(a.out, " (temporary above %d MB)", st.TemporaryThresholdMB)
		}
		fmt.Fprintln(a.out)
		fmt.Fprintf(a.out, "bench xfer: %s\n", emptyDash(st.BenchmarkTransferMode))
		fmt.Fprintf(a.out, "cache    : %s\n", emptyDash(st.ModelCache))
		fmt.Fprintf(a.out, "warm     : scaledown=%ds min_containers=%d\n", st.ScaledownWindowSeconds, st.MinContainers)
		fmt.Fprintf(a.out, "audio    : %s\n", st.UserDataPolicy)
		fmt.Fprintf(a.out, "bench    : %s\n", st.BenchmarkDataPolicy)
		fmt.Fprintf(a.out, "models   : %s\n", st.ModelDataPolicy)
		if st.Message != "" {
			fmt.Fprintf(a.out, "note     : %s\n", st.Message)
		}
		return 0
	case "setup":
		fs := flag.NewFlagSet("modal setup", flag.ContinueOnError)
		tokenID := fs.String("token-id", os.Getenv("MODAL_TOKEN_ID"), "Modal token id")
		tokenSecret := fs.String("token-secret", os.Getenv("MODAL_TOKEN_SECRET"), "Modal token secret")
		endpoint := fs.String("endpoint-url", firstEnv("NOTO_MODAL_ENDPOINT_URL", "MODAL_ENDPOINT_URL"), "Modal Web Function URL")
		endpointToken := fs.String("endpoint-token", os.Getenv("NOTO_MODAL_ENDPOINT_TOKEN"), "bearer token for the deployed Modal endpoint")
		appName := fs.String("app", firstEnv("NOTO_MODAL_APP_NAME", "MODAL_APP_NAME"), "Modal app name")
		environment := fs.String("env", firstEnv("NOTO_MODAL_ENVIRONMENT", "MODAL_ENVIRONMENT"), "Modal environment")
		gpu := fs.String("gpu", firstEnv("NOTO_MODAL_GPU", "MODAL_GPU"), "Modal GPU class")
		userTransfer := fs.String("user-transfer", firstEnv("NOTO_MODAL_USER_TRANSFER_MODE", "MODAL_USER_TRANSFER_MODE"), "user transfer mode: http|temporary|permanent")
		benchTransfer := fs.String("benchmark-transfer", firstEnv("NOTO_MODAL_BENCHMARK_TRANSFER_MODE", "MODAL_BENCHMARK_TRANSFER_MODE"), "benchmark transfer mode: http|temporary|permanent")
		tempThreshold := fs.Int("temporary-threshold-mb", firstEnvInt("NOTO_MODAL_TEMPORARY_THRESHOLD_MB", "MODAL_TEMPORARY_THRESHOLD_MB"), "switch to temporary staging above this size")
		scaledownWindow := fs.Int("scaledown-window-seconds", firstEnvInt("NOTO_MODAL_SCALEDOWN_WINDOW_SECONDS", "MODAL_SCALEDOWN_WINDOW_SECONDS"), "seconds Modal keeps an idle container warm")
		minContainers := fs.Int("min-containers", firstEnvInt("NOTO_MODAL_MIN_CONTAINERS", "MODAL_MIN_CONTAINERS"), "warm containers to keep running")
		modelCache := fs.String("model-cache", firstEnv("NOTO_MODAL_MODEL_CACHE", "MODAL_MODEL_CACHE"), "model cache mode: volume|image")
		noRoutes := fs.Bool("no-routes", false, "store config but do not route speech/diarization to Modal")
		if err := fs.Parse(args[1:]); err != nil {
			return 64
		}
		st, err := client.SetupModalCompute(ctx, notoapi.ModalSetupRequest{
			TokenID:                *tokenID,
			TokenSecret:            *tokenSecret,
			EndpointURL:            *endpoint,
			EndpointToken:          *endpointToken,
			AppName:                *appName,
			Environment:            *environment,
			GPU:                    *gpu,
			UserTransferMode:       *userTransfer,
			BenchmarkTransferMode:  *benchTransfer,
			TemporaryThresholdMB:   *tempThreshold,
			ScaledownWindowSeconds: *scaledownWindow,
			MinContainers:          *minContainers,
			ModelCache:             *modelCache,
			ConfigureRoutes:        !*noRoutes,
		})
		if err != nil {
			return a.errExit(err)
		}
		return a.emitJSON(st)
	case "benchmark":
		fs := flag.NewFlagSet("modal benchmark", flag.ContinueOnError)
		suite := fs.String("suite", "e2e", "benchmark suite")
		hours := fs.Float64("hours", 0, "audio hours cap")
		format := fs.String("format", "json", "result format")
		if err := fs.Parse(args[1:]); err != nil {
			return 64
		}
		res, err := client.RunModalBenchmark(ctx, notoapi.ModalBenchmarkRequest{Suite: *suite, Hours: *hours, Format: *format})
		if err != nil {
			return a.errExit(err)
		}
		return a.emitJSON(res)
	default:
		fmt.Fprintln(a.errOut, "noto modal [status|setup|benchmark]")
		return 64
	}
}

func firstEnv(names ...string) string {
	for _, n := range names {
		if v := os.Getenv(n); v != "" {
			return v
		}
	}
	return ""
}

func firstEnvInt(names ...string) int {
	for _, n := range names {
		if v := os.Getenv(n); v != "" {
			var out int
			if _, err := fmt.Sscanf(v, "%d", &out); err == nil {
				return out
			}
		}
	}
	return 0
}

func emptyDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

func (a *app) runDev(args []string) int {
	// `noto dev` runs the TUI against an in-process server. It also
	// pre-warms the capture helper so the first hit to the recorder
	// screen is instant rather than waiting on `swift build` / IPC
	// handshake. Linux/Windows preflight is a no-op.
	ctx, cancel := signalContext()
	defer cancel()
	host, err := host.Start(ctx, host.Options{Version: "0.1.0-dev"})
	if err != nil {
		fmt.Fprintf(a.errOut, "noto dev: %v\n", err)
		return 1
	}
	defer host.Close()
	paths, _ := host.Client().GetPaths(ctx)
	fmt.Fprintf(a.errOut, "noto dev  config=%s recordings=%s socket=%s\n",
		paths.ConfigDir, paths.RecordingsDir, host.SocketPath())

	// Pre-warm capture helper (best-effort).
	go func() {
		pfCtx, pfCancel := context.WithTimeout(ctx, 5*time.Second)
		defer pfCancel()
		if res, err := host.Client().PreflightRecording(pfCtx); err == nil {
			if res.MicReady {
				fmt.Fprintln(a.errOut, "noto dev  capture helper ready")
			} else if res.Diagnostic != "" {
				fmt.Fprintf(a.errOut, "noto dev  capture: %s\n", res.Diagnostic)
			}
		}
	}()

	if len(args) > 0 && args[0] == "serve" {
		fmt.Fprintln(a.errOut, "noto dev  press Ctrl+C to stop")
		<-ctx.Done()
		return 0
	}
	if err := tui.Run(ctx, host.Client()); err != nil {
		fmt.Fprintf(a.errOut, "noto dev: %v\n", err)
		return 1
	}
	return 0
}

func (a *app) runStop(args []string) int {
	ctx, cancel := defaultCtx()
	defer cancel()
	client, closer, code := a.connect(ctx)
	if code != 0 {
		return code
	}
	defer closer()
	res, err := client.StopRecording(ctx, notoapi.StopRecordingOpts{})
	if err != nil {
		return a.errExit(err)
	}
	return a.emitJSON(res)
}

func (a *app) runJobs(args []string) int {
	ctx, cancel := defaultCtx()
	defer cancel()
	client, closer, code := a.connect(ctx)
	if code != 0 {
		return code
	}
	defer closer()
	jobs, err := client.ListJobs(ctx, notoapi.ListJobsOpts{Limit: 50})
	if err != nil {
		return a.errExit(err)
	}
	if hasFlag(args, "--json") {
		return a.emitJSON(jobs)
	}
	if len(jobs) == 0 {
		fmt.Fprintln(a.out, "no jobs")
		return 0
	}
	for _, j := range jobs {
		fmt.Fprintf(a.out, "%-12s  %-10s  %-10s  %3.0f%%  %s\n", j.Kind, j.Status, j.Phase, j.Progress*100, j.ID)
	}
	return 0
}

func (a *app) runPing(args []string) int {
	ctx, cancel := defaultCtx()
	defer cancel()
	client, closer, code := a.connect(ctx)
	if code != 0 {
		return code
	}
	defer closer()
	h, err := client.Health(ctx)
	if err != nil {
		return a.errExit(err)
	}
	sys, _ := client.GetSystem(ctx)
	return a.emitJSON(map[string]any{"health": h, "system": sys})
}

// --- Seed (dev-only) ---

// runSeed inserts the bundled fixture meetings into the local store and
// search index. It always uses an in-process host so it works even when
// the daemon isn't running. Refuses to act against a remote backend.
func (a *app) runSeed(_ []string) int {
	if os.Getenv("NOTO_API_URL") != "" {
		fmt.Fprintln(a.errOut, "noto seed: refusing to seed against a remote NOTO_API_URL")
		return 1
	}
	ctx, cancel := defaultCtx()
	defer cancel()
	host, err := host.Start(ctx, host.Options{Version: "seed"})
	if err != nil {
		fmt.Fprintf(a.errOut, "noto seed: %v\n", err)
		return 1
	}
	defer host.Close()
	seeded, err := host.SeedDev(ctx)
	if err != nil {
		fmt.Fprintf(a.errOut, "noto seed: %v\n", err)
		return 1
	}
	fmt.Fprintf(a.out, "seeded %d meetings:\n", len(seeded.Meetings))
	for _, m := range seeded.Meetings {
		fmt.Fprintf(a.out, "  • %s — %s\n      %s\n", m.ID, m.Title, m.Dir)
	}
	if seeded.People > 0 {
		fmt.Fprintf(a.out, "seeded %d people into the People directory (press 2 in the TUI)\n", seeded.People)
	}
	return 0
}

// runReset wipes ALL local meetings and people back to an empty store. Like
// seed it always uses an in-process host and refuses against a remote backend.
// Because it's destructive it requires an explicit confirmation: pass --yes, or
// type "yes" at the prompt.
func (a *app) runReset(args []string) int {
	if os.Getenv("NOTO_API_URL") != "" {
		fmt.Fprintln(a.errOut, "noto reset: refusing to wipe a remote NOTO_API_URL")
		return 1
	}
	if !hasFlag(args, "--yes") && !hasFlag(args, "-y") {
		fmt.Fprint(a.out, "This deletes ALL local meetings and people. Type 'yes' to continue: ")
		reader := bufio.NewReader(a.in)
		line, _ := reader.ReadString('\n')
		if strings.TrimSpace(line) != "yes" {
			fmt.Fprintln(a.out, "aborted")
			return 0
		}
	}
	ctx, cancel := defaultCtx()
	defer cancel()
	host, err := host.Start(ctx, host.Options{Version: "reset"})
	if err != nil {
		fmt.Fprintf(a.errOut, "noto reset: %v\n", err)
		return 1
	}
	defer host.Close()
	res, err := host.PurgeAll(ctx)
	if err != nil {
		fmt.Fprintf(a.errOut, "noto reset: %v\n", err)
		return 1
	}
	fmt.Fprintf(a.out, "removed %d meetings and %d people — store is empty\n", res.Meetings, res.People)
	return 0
}
