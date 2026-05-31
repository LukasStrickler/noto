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

	tea "github.com/charmbracelet/bubbletea"
	"github.com/lukasstrickler/noto/internal/notoapi"
)

// Run is the entrypoint called by cmd/noto via internal/cli.
// It blocks until the user quits.
func Run(ctx context.Context, client notoapi.Client) error {
	if client == nil {
		return fmt.Errorf("tui: client is nil")
	}
	model := newRootModel(ctx, client)
	prog := tea.NewProgram(model,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)
	go func() {
		<-ctx.Done()
		prog.Quit()
	}()
	_, err := prog.Run()
	return err
}
