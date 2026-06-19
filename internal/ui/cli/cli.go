// Package cli is the noto command surface. Every verb is a thin
// wrapper around a notoapi.Client; we never reach into storage/search
// directly. The lifecycle decision (in-process server vs. talk to a
// remote/serve daemon) is owned by internal/app/host.
//
// The package is split by concern: this file owns the dispatch table and
// the shared app/connect plumbing; commands_meetings.go and
// commands_system.go hold the verb handlers; helpers.go holds the
// render/arg utilities they share.
package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/lukasstrickler/noto/internal/app/host"
	"github.com/lukasstrickler/noto/internal/transport/notoapi"
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
	case "speaker-model":
		return app.runSpeakerModel(args[1:])
	case "models":
		return app.runModels(args[1:])
	case "modal":
		return app.runModal(args[1:])
	case "bench":
		return app.runBench(args[1:])
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
	case "reset":
		return app.runReset(args[1:])
	default:
		fmt.Fprintf(errOut, "noto: unknown command %q. Run `noto help`.\n", args[0])
		return 64
	}
}

type app struct {
	in     io.Reader
	out    io.Writer
	errOut io.Writer

	// connectFn, when set, overrides how commands obtain a client. Tests
	// inject an in-memory client here so the full command path (flag parse
	// → client call → JSON/text render) runs without a real backend.
	connectFn func(ctx context.Context) (notoapi.Client, func(), int)
}

// connect wires up either an in-process backend or a remote one.
// Callers MUST call the returned closer.
func (a *app) connect(ctx context.Context) (notoapi.Client, func(), int) {
	if a.connectFn != nil {
		return a.connectFn(ctx)
	}
	client, closer, err := host.Connect(ctx, host.Options{Version: "0.1.0"})
	if err != nil {
		fmt.Fprintf(a.errOut, "noto: %v\n", err)
		return nil, nil, 1
	}
	if closer == nil {
		closer = func() {}
	}
	return client, closer, 0
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
  noto models [status|download <id>]
                          Manage local STT/diarization models (backend-aware)
  noto modal [status|setup|benchmark]
                          Configure Modal GPU compute (no Modal CLI required)
  noto bench [run|preflight|compare|audit|dataset|ledger]
                          Benchmark measurement spine (cost/quality compare)
  noto speaker-model [status|download]
                          Manage the local voiceprint (ECAPA) model
  noto record [--title "..."]
  noto stop
  noto import-audio <path> [--title "..."] [--wait] [--json]
  noto jobs [--json]
  noto ping
  noto seed                Insert fixture meetings + a People directory (dev-only)
  noto reset [--yes]       Wipe ALL local meetings + people (dev-only)
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
