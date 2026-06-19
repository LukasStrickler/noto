package tui

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/lukasstrickler/noto/internal/transport/notoapi"
	"github.com/lukasstrickler/noto/internal/ui/tui/theme"
)

// TestRenderTopologyDebug is a manual inspection harness: `go test -run
// TestRenderTopologyDebug -v ./internal/ui/tui/`. It prints each topology at
// several widths (including the panel minimum) and flags any line that overruns
// the target width — the exact failure mode to avoid.
func TestRenderTopologyDebug(t *testing.T) {
	fmt.Printf("minContentW=%d minSidebarW=%d\n", minContentW, minSidebarW)
	s := theme.NewStyles()

	scenarios := map[string]struct {
		sys           notoapi.System
		backendRemote bool
	}{
		"local-single": {notoapi.System{Hostname: "macbook", Accelerator: "coreml", Compute: localCompute(), DataPlane: notoapi.DataPlane{Location: "local", Storage: "local"}}, false},
		"offload-modal": {notoapi.System{Hostname: "mac", Compute: notoapi.ComputeTopology{
			Speech:  notoapi.ComputePlacement{Location: "remote", Endpoint: "noto--stt.modal.run", Trust: notoapi.TrustCloud},
			Diarize: notoapi.ComputePlacement{Location: "remote", Endpoint: "noto--stt.modal.run", Trust: notoapi.TrustCloud},
			Embed:   notoapi.ComputePlacement{Location: "local"}}, DataPlane: notoapi.DataPlane{Location: "local", Storage: "local"}}, false},
		"api-server": {notoapi.System{Compute: notoapi.ComputeTopology{
			Speech:  notoapi.ComputePlacement{Location: "remote", Endpoint: "gpu-pool.modal.run", Trust: notoapi.TrustCloud},
			Diarize: notoapi.ComputePlacement{Location: "remote", Endpoint: "gpu-pool.modal.run", Trust: notoapi.TrustCloud},
			Embed:   notoapi.ComputePlacement{Location: "local"}}, DataPlane: notoapi.DataPlane{Location: "remote", Endpoint: "noto.my-server.example.com:8731", Storage: "remote", Trust: notoapi.TrustOwned}}, false},
		"thin-client": {notoapi.System{Hostname: "noto.my-server.example.com", Compute: localCompute(), DataPlane: notoapi.DataPlane{Location: "local", Storage: "local"}}, true},
	}

	widths := []int{minContentW - 4, 74, 110}
	for name, sc := range scenarios {
		for _, w := range widths {
			out := renderTopology(sc.sys, s, w, 2, sc.backendRemote)
			plain := ansi.Strip(out)
			over := 0
			for _, ln := range strings.Split(plain, "\n") {
				if lipgloss.Width(ln) > w {
					over++
				}
			}
			if testing.Verbose() {
				fmt.Printf("\n===== %s @ width=%d  (overflow lines: %d) =====\n%s\n", name, w, over, plain)
			}
			if over > 0 {
				t.Errorf("%s @ width=%d has %d line(s) exceeding the target width", name, w, over)
			}
		}
	}
}
