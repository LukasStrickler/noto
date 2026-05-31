package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lukasstrickler/noto/internal/app/host"
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
	out := map[string]any{
		"health":    h,
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
	return a.emitJSON(h)
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
	fmt.Fprintf(a.out, "seeded %d meetings:\n", len(seeded))
	for _, m := range seeded {
		fmt.Fprintf(a.out, "  • %s — %s\n      %s\n", m.ID, m.Title, m.Dir)
	}
	return 0
}
