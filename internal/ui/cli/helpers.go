package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/lukasstrickler/noto/internal/app/host"
	"github.com/lukasstrickler/noto/internal/transport/notoapi"
)

// Shared plumbing for the command handlers: context construction, JSON/text
// rendering, error exit, and the small arg-parsing helpers.

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

// emitOrText centralizes the "--json prints the envelope, otherwise render
// human text" decision so a command can't accidentally ship without --json
// support. text runs only in the non-JSON path.
func (a *app) emitOrText(args []string, v any, text func()) int {
	if hasFlag(args, "--json") {
		return a.emitJSON(v)
	}
	text()
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
	return host.NewToken()
}
