// Package cli is the noto command surface. Every verb is a thin
// wrapper around a notoapi.Client; we never reach into storage/search
// directly. The lifecycle decision (in-process server vs. talk to a
// remote/serve daemon) is owned by internal/notohost.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/lukasstrickler/noto/internal/notoapi"
	"github.com/lukasstrickler/noto/internal/notohost"
	"github.com/lukasstrickler/noto/internal/tui"
)

// Run is the binary entry point.
func Run(args []string, in io.Reader, out io.Writer, errOut io.Writer) int {
	app := &app{in: in, out: out, errOut: errOut}
	if len(args) == 0 {
		return app.runTUI(args)
	}
	switch args[0] {
	case "help", "--help", "-h":
		printHelp(out)
		return 0
	case "version", "--version":
		fmt.Fprintln(out, "noto 0.1.0 (refactor)")
		return 0
	case "tui":
		return app.runTUI(args[1:])
	case "serve":
		return app.runServe(args[1:])
	case "list":
		return app.runList(args[1:])
	case "search":
		return app.runSearch(args[1:])
	case "show":
		return app.runShow(args[1:])
	case "transcript":
		return app.runTranscript(args[1:])
	case "summary":
		return app.runSummary(args[1:])
	case "files":
		return app.runFiles(args[1:])
	case "agent":
		return app.runAgent(args[1:])
	case "status":
		return app.runStatus(args[1:])
	case "providers":
		return app.runProviders(args[1:])
	case "verify":
		return app.runVerify(args[1:])
	case "record":
		return app.runRecord(args[1:])
	case "import-audio":
		return app.runImportAudio(args[1:])
	case "stop":
		return app.runStop(args[1:])
	case "dev":
		return app.runDev(args[1:])
	case "jobs":
		return app.runJobs(args[1:])
	case "ping":
		return app.runPing(args[1:])
	case "seed":
		return app.runSeed(args[1:])
	default:
		fmt.Fprintf(errOut, "noto: unknown command %q. Run `noto help`.\n", args[0])
		return 64
	}
}

type app struct {
	in     io.Reader
	out    io.Writer
	errOut io.Writer
}

// connect wires up either an in-process backend or a remote one.
// Callers MUST call the returned closer.
func (a *app) connect(ctx context.Context) (notoapi.Client, func(), int) {
	client, closer, err := notohost.Connect(ctx, notohost.Options{Version: "0.1.0"})
	if err != nil {
		fmt.Fprintf(a.errOut, "noto: %v\n", err)
		return nil, nil, 1
	}
	if closer == nil {
		closer = func() {}
	}
	return client, closer, 0
}

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

	host, err := notohost.Start(ctx, notohost.Options{
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

// --- list ---

func (a *app) runList(args []string) int {
	asJSON := hasFlag(args, "--json")
	ctx, cancel := defaultCtx()
	defer cancel()
	client, closer, code := a.connect(ctx)
	if code != 0 {
		return code
	}
	defer closer()
	res, err := client.ListMeetings(ctx, notoapi.ListMeetingsOpts{})
	if err != nil {
		return a.errExit(err)
	}
	if asJSON {
		return a.emitJSON(res)
	}
	if len(res.Meetings) == 0 {
		fmt.Fprintln(a.out, "No meetings yet. Try `noto tui` and press r to record.")
		return 0
	}
	fmt.Fprintf(a.out, "%-36s  %-30s  %s\n", "ID", "TITLE", "STATUS")
	for _, m := range res.Meetings {
		title := m.Title
		if len(title) > 30 {
			title = title[:27] + "…"
		}
		fmt.Fprintf(a.out, "%-36s  %-30s  %s\n", m.ID, title, m.Status)
	}
	return 0
}

// --- search ---

func (a *app) runSearch(args []string) int {
	asJSON := hasFlag(args, "--json")
	q := stripFlags(args)
	if q == "" {
		fmt.Fprintln(a.errOut, "noto search: query required")
		return 64
	}
	ctx, cancel := defaultCtx()
	defer cancel()
	client, closer, code := a.connect(ctx)
	if code != 0 {
		return code
	}
	defer closer()
	res, err := client.Search(ctx, notoapi.SearchOpts{Query: q})
	if err != nil {
		return a.errExit(err)
	}
	if asJSON {
		return a.emitJSON(res)
	}
	if len(res.Hits) == 0 {
		fmt.Fprintf(a.out, "no matches for %q\n", q)
		return 0
	}
	for _, h := range res.Hits {
		fmt.Fprintf(a.out, "%s  [%.0fs] %s — %s\n", h.MeetingID, h.Timestamp, h.Speaker, trim(h.Snippet, 80))
	}
	return 0
}

// --- show / transcript / summary / files / agent ---

func (a *app) runShow(args []string) int {
	asJSON := hasFlag(args, "--json")
	id := stripFlags(args)
	if id == "" {
		fmt.Fprintln(a.errOut, "noto show: meeting id required")
		return 64
	}
	ctx, cancel := defaultCtx()
	defer cancel()
	client, closer, code := a.connect(ctx)
	if code != 0 {
		return code
	}
	defer closer()
	m, err := client.GetMeeting(ctx, id)
	if err != nil {
		return a.errExit(err)
	}
	if asJSON {
		return a.emitJSON(m)
	}
	fmt.Fprintf(a.out, "%s\n", m.Title)
	fmt.Fprintf(a.out, "  id            %s\n", m.ID)
	fmt.Fprintf(a.out, "  status        %s\n", m.Status)
	fmt.Fprintf(a.out, "  duration_sec  %d\n", m.DurationSeconds)
	fmt.Fprintf(a.out, "  decisions     %d\n", m.DecisionCount)
	fmt.Fprintf(a.out, "  action items  %d\n", m.ActionCount)
	fmt.Fprintf(a.out, "  risks         %d\n", m.RiskCount)
	if m.ShortSummary != "" {
		fmt.Fprintf(a.out, "\n%s\n", m.ShortSummary)
	}
	return 0
}

func (a *app) runTranscript(args []string) int {
	id := stripFlags(args)
	if id == "" {
		fmt.Fprintln(a.errOut, "noto transcript: meeting id required")
		return 64
	}
	ctx, cancel := defaultCtx()
	defer cancel()
	client, closer, code := a.connect(ctx)
	if code != 0 {
		return code
	}
	defer closer()
	t, err := client.GetTranscript(ctx, id)
	if err != nil {
		return a.errExit(err)
	}
	if hasFlag(args, "--json") {
		return a.emitJSON(t)
	}
	for _, seg := range t.Segments {
		fmt.Fprintf(a.out, "[%6.1fs] %s [%s]: %s\n", seg.StartSec, seg.Speaker, seg.Role, seg.Text)
	}
	return 0
}

func (a *app) runSummary(args []string) int {
	id := stripFlags(args)
	if id == "" {
		fmt.Fprintln(a.errOut, "noto summary: meeting id required")
		return 64
	}
	ctx, cancel := defaultCtx()
	defer cancel()
	client, closer, code := a.connect(ctx)
	if code != 0 {
		return code
	}
	defer closer()
	s, err := client.GetSummary(ctx, id)
	if err != nil {
		return a.errExit(err)
	}
	if hasFlag(args, "--json") {
		return a.emitJSON(s)
	}
	if s.Markdown != "" {
		fmt.Fprintln(a.out, s.Markdown)
		return 0
	}
	fmt.Fprintf(a.out, "%s\n", s.ShortSummary)
	return 0
}

func (a *app) runFiles(args []string) int {
	id := stripFlags(args)
	if id == "" {
		fmt.Fprintln(a.errOut, "noto files: meeting id required")
		return 64
	}
	ctx, cancel := defaultCtx()
	defer cancel()
	client, closer, code := a.connect(ctx)
	if code != 0 {
		return code
	}
	defer closer()
	f, err := client.GetMeetingFiles(ctx, id)
	if err != nil {
		return a.errExit(err)
	}
	return a.emitJSON(f)
}

func (a *app) runAgent(args []string) int {
	id := stripFlags(args)
	if id == "" {
		fmt.Fprintln(a.errOut, "noto agent: meeting id required")
		return 64
	}
	ctx, cancel := defaultCtx()
	defer cancel()
	client, closer, code := a.connect(ctx)
	if code != 0 {
		return code
	}
	defer closer()
	h, err := client.GetAgentHandoff(ctx, id)
	if err != nil {
		return a.errExit(err)
	}
	return a.emitJSON(h)
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
		Title:   *title,
		Sources: []string{"microphone", "system_audio"},
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
	host, err := notohost.Start(ctx, notohost.Options{Version: "0.1.0-dev"})
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
	host, err := notohost.Start(ctx, notohost.Options{Version: "seed"})
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

// --- helpers ---

func defaultCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 60*time.Second)
}

func signalContext() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()
	return ctx, cancel
}

func (a *app) emitJSON(v any) int {
	enc := json.NewEncoder(a.out)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		fmt.Fprintf(a.errOut, "noto: emit: %v\n", err)
		return 1
	}
	return 0
}

func (a *app) errExit(err error) int {
	if apiErr, ok := notoapi.As(err); ok {
		_ = json.NewEncoder(a.errOut).Encode(notoapi.ErrorEnvelope{Error: apiErr})
		return 1
	}
	fmt.Fprintf(a.errOut, "noto: %v\n", err)
	return 1
}

// pickPositional removes the first non-flag argument from args and
// returns it. The remaining args (still containing all -flag/--flag
// tokens in their original order) can be passed to flag.FlagSet.Parse.
// This lets `noto import-audio --title "x" /path/file.m4a --wait` and
// `noto import-audio /path/file.m4a --title "x" --wait` behave the same.
func pickPositional(args []string) (string, []string) {
	skipNext := false
	for i, a := range args {
		if skipNext {
			skipNext = false
			continue
		}
		if !strings.HasPrefix(a, "-") {
			rest := make([]string, 0, len(args)-1)
			rest = append(rest, args[:i]...)
			rest = append(rest, args[i+1:]...)
			return a, rest
		}
		// `--flag value` form: consume the value too.
		if a == "--title" || a == "-title" {
			skipNext = true
		}
	}
	return "", args
}

func hasFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

func stripFlags(args []string) string {
	for _, a := range args {
		if !strings.HasPrefix(a, "--") {
			return a
		}
	}
	return ""
}

func trim(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

func randomToken() string {
	return notohost.NewToken()
}

func printHelp(out io.Writer) {
	fmt.Fprint(out, `noto — terminal-first meeting recorder

Usage:
  noto                    Open the TUI
  noto tui                Same as noto
  noto serve [--listen unix:<path>|tcp:<host:port>] [--token-file <path>]
                          Run the noto backend daemon
  noto list [--json]
  noto search <query> [--json]
  noto show <id> [--json]
  noto transcript <id> [--json]
  noto summary <id> [--json]
  noto files <id>
  noto agent <id>
  noto status
  noto providers [list|key-set|key-remove|test|active-speech|active-llm] ...
  noto verify
  noto record [--title "..."]
  noto stop
  noto import-audio <path> [--title "..."] [--wait] [--json]
  noto jobs [--json]
  noto ping
  noto seed                Insert fixture meetings (dev-only, local backend)
  noto dev [serve]     Run from a checked-out repo (alias of TUI, prints debug paths)

Local lifecycle:
  - Plain noto/CLI commands auto-spawn an in-process server when needed
    and shut it down on exit. Idle cost ~ 0.
  - Set NOTO_API_URL (and NOTO_API_TOKEN for TCP) to talk to a remote
    noto. Recommended pattern: run
        noto serve --listen tcp:0.0.0.0:8731
    on the remote machine, port-forward, then export
        NOTO_API_URL=http://localhost:8731
        NOTO_API_TOKEN=<token>
    on the client.
`)
}
