// Package tui is the noto terminal UI. The top-level layout is:
//
//	┌── header ─────────────────────────────────────────────────┐
//	│ noto · screen title · breadcrumbs                          │
//	├── content (active screen's View) ──────────────────────────┤
//	│ ...                                                        │
//	├── status bar (1 row, always visible, fed by SSE) ──────────┤
//	│ REC · idx · jobs · meeting count · hint cluster            │
//	└────────────────────────────────────────────────────────────┘
//
// Every screen plugs into the same Screen interface; the router holds
// a small stack so Esc always pops. A command palette is the primary
// nav: ":" then fuzzy match.
package tui

import (
	"context"
	"fmt"

	tea "charm.land/bubbletea/v2"
	"github.com/lukasstrickler/noto/internal/transport/notoapi"
)

// Run is the entrypoint called by cmd/noto via internal/cli.
// It blocks until the user quits.
func Run(ctx context.Context, client notoapi.Client) error {
	if client == nil {
		return fmt.Errorf("tui: client is nil")
	}
	model := newRootModel(ctx, client)
	// v2: alt-screen and mouse mode are declared on the View
	// (see rootModel.View), not as program options.
	prog := tea.NewProgram(model)
	go func() {
		<-ctx.Done()
		prog.Quit()
	}()
	_, err := prog.Run()
	return err
}
