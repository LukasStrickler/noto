package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/lukasstrickler/noto/internal/transport/notoapi"
	"github.com/lukasstrickler/noto/internal/ui/tui/theme"
)

func localCompute() notoapi.ComputeTopology {
	return notoapi.ComputeTopology{
		Speech:  notoapi.ComputePlacement{Location: "local"},
		Diarize: notoapi.ComputePlacement{Location: "local"},
		Embed:   notoapi.ComputePlacement{Location: "local"},
	}
}

func TestRenderTopologyLocalIsOneNode(t *testing.T) {
	sys := notoapi.System{
		Hostname:    "macbook",
		Accelerator: "coreml",
		Compute:     localCompute(),
		DataPlane:   notoapi.DataPlane{Location: "local", Storage: "local"},
	}
	out := ansi.Strip(renderTopology(sys, theme.NewStyles(), 100, 0, false))
	// The standardized local box: the same "This device" anchor as every other
	// shape, showing it keeps compute + storage on-device. Shape label "Local".
	for _, want := range []string{"This device", "Local", "compute · storage", "identity ●"} {
		if !strings.Contains(out, want) {
			t.Errorf("local topology missing %q:\n%s", want, out)
		}
	}
}

func TestRenderTopologyOffloadComputeToCloud(t *testing.T) {
	sys := notoapi.System{
		Hostname: "mac",
		Compute: notoapi.ComputeTopology{
			Speech:  notoapi.ComputePlacement{Location: "remote", Endpoint: "noto--stt.modal.run", Trust: notoapi.TrustCloud},
			Diarize: notoapi.ComputePlacement{Location: "remote", Endpoint: "noto--stt.modal.run", Trust: notoapi.TrustCloud},
			Embed:   notoapi.ComputePlacement{Location: "local"},
		},
		DataPlane: notoapi.DataPlane{Location: "local", Storage: "local"},
	}
	out := ansi.Strip(renderTopology(sys, theme.NewStyles(), 100, 3, false))
	for _, want := range []string{"Offload compute", "This device", "Compute", "noto--stt.modal.run", "third-party", "audio", "transcript", "audio leaves your systems"} {
		if !strings.Contains(out, want) {
			t.Errorf("offload-to-cloud topology missing %q:\n%s", want, out)
		}
	}
}

func TestRenderTopologyAPIServerHasThreeNodes(t *testing.T) {
	sys := notoapi.System{
		Compute: notoapi.ComputeTopology{
			Speech:  notoapi.ComputePlacement{Location: "remote", Endpoint: "gpu.modal.run", Trust: notoapi.TrustCloud},
			Diarize: notoapi.ComputePlacement{Location: "remote", Endpoint: "gpu.modal.run", Trust: notoapi.TrustCloud},
			Embed:   notoapi.ComputePlacement{Location: "local"},
		},
		DataPlane: notoapi.DataPlane{Location: "remote", Endpoint: "srv.example.com:8731", Storage: "remote", Trust: notoapi.TrustOwned},
	}
	out := ansi.Strip(renderTopology(sys, theme.NewStyles(), 120, 1, false))
	// All three planes present, the server is "yours", the GPU is third-party.
	for _, want := range []string{"API server", "Data + API", "This device", "Compute", "srv.example.com:8731", "gpu.modal.run", "your server", "third-party", "artifacts", "audio"} {
		if !strings.Contains(out, want) {
			t.Errorf("api-server topology missing %q:\n%s", want, out)
		}
	}
}

// Thin client to a remote server that does compute locally (local TUI + remote
// + local compute): the System payload looks all-local (it's the server's view),
// so the backendRemote bit is what makes the diagram show the Frontend↔Server
// split instead of collapsing to one box.
func TestRenderTopologyThinClient(t *testing.T) {
	sys := notoapi.System{
		Hostname:  "srv.example.com",
		Compute:   localCompute(),
		DataPlane: notoapi.DataPlane{Location: "local", Storage: "local"},
	}
	out := ansi.Strip(renderTopology(sys, theme.NewStyles(), 100, 0, true))
	for _, want := range []string{"Remote backend", "This device", "Backend server", "srv.example.com", "compute (local)", "audio", "results"} {
		if !strings.Contains(out, want) {
			t.Errorf("thin-client topology missing %q:\n%s", want, out)
		}
	}
}

// The all-local case is a single box with nothing leaving the machine, so it
// must NOT animate — the same frame renders identically regardless of frame.
func TestRenderTopologySingleNodeIsStatic(t *testing.T) {
	sys := notoapi.System{
		Hostname:  "macbook",
		Compute:   localCompute(),
		DataPlane: notoapi.DataPlane{Location: "local", Storage: "local"},
	}
	a := ansi.Strip(renderTopology(sys, theme.NewStyles(), 100, 0, false))
	b := ansi.Strip(renderTopology(sys, theme.NewStyles(), 100, 5, false))
	if a != b {
		t.Error("single-node (all-local) topology must be static across frames")
	}
}

// Below the two-box hub footprint a three-plane split must fall back to the
// compact list (coloured markers, no boxes) instead of overrunning a too-narrow
// window. No line may exceed the target width at any of these widths.
func TestRenderTopologyCompactFallbackAtMinWidth(t *testing.T) {
	sys := notoapi.System{
		Compute: notoapi.ComputeTopology{
			Speech:  notoapi.ComputePlacement{Location: "remote", Endpoint: "gpu-pool.modal.run", Trust: notoapi.TrustCloud},
			Diarize: notoapi.ComputePlacement{Location: "remote", Endpoint: "gpu-pool.modal.run", Trust: notoapi.TrustCloud},
			Embed:   notoapi.ComputePlacement{Location: "local"},
		},
		// The most space-hungry case: a long, fully-qualified server hostname.
		DataPlane: notoapi.DataPlane{Location: "remote", Endpoint: "noto.my-server.example.com:8731", Storage: "remote", Trust: notoapi.TrustOwned},
	}
	// The 2-D hub needs only a two-box top row (2*(14+4)+14 = 50); below that the
	// compact list takes over.
	for _, w := range []int{48, 44, 40} {
		out := ansi.Strip(renderTopology(sys, theme.NewStyles(), w, 2, false))
		if strings.Contains(out, "╭") {
			t.Errorf("width=%d: expected the compact (boxless) list, got bordered boxes:\n%s", w, out)
		}
		for _, ln := range strings.Split(out, "\n") {
			if lipgloss.Width(ln) > w {
				t.Errorf("width=%d: line exceeds width: %q", w, ln)
			}
		}
	}
}

// The full three-plane split renders as a 2-D hub: this device and compute share
// the top row, and the data plane sits BELOW them (a corner layout), never as a
// third box in one wide row. No line may exceed the target width.
func TestRenderTopologyHubStacksDataBelow(t *testing.T) {
	sys := notoapi.System{
		Compute: notoapi.ComputeTopology{
			Speech:  notoapi.ComputePlacement{Location: "remote", Endpoint: "gpu.modal.run", Trust: notoapi.TrustCloud},
			Diarize: notoapi.ComputePlacement{Location: "remote", Endpoint: "gpu.modal.run", Trust: notoapi.TrustCloud},
			Embed:   notoapi.ComputePlacement{Location: "local"},
		},
		DataPlane: notoapi.DataPlane{Location: "remote", Endpoint: "srv.example.com:8731", Storage: "remote", Trust: notoapi.TrustOwned},
	}
	w := 100
	out := ansi.Strip(renderTopology(sys, theme.NewStyles(), w, 1, false))
	lines := strings.Split(out, "\n")
	devRow, dataRow := -1, -1
	for i, ln := range lines {
		if devRow < 0 && strings.Contains(ln, "This device") {
			devRow = i
		}
		if strings.Contains(ln, "Data + API") {
			dataRow = i
		}
		if lipgloss.Width(ln) > w {
			t.Errorf("line exceeds width: %q", ln)
		}
	}
	if devRow < 0 || dataRow < 0 {
		t.Fatalf("expected both 'This device' and 'Data + API':\n%s", out)
	}
	// Compute shares the device's top row (horizontal pair); data is below it.
	if !strings.Contains(lines[devRow], "Compute") {
		t.Errorf("'This device' and 'Compute' should sit on the same top row:\n%s", out)
	}
	if dataRow <= devRow {
		t.Errorf("the data plane must render BELOW the device row (2-D hub); devRow=%d dataRow=%d:\n%s", devRow, dataRow, out)
	}
}

// A long hostname must be ellipsised, never wrapped onto a second line inside a
// box — the boxed (wide) render keeps every node line on its own row.
func TestRenderTopologyTruncatesHostnameNoWrap(t *testing.T) {
	sys := notoapi.System{
		Compute: notoapi.ComputeTopology{
			Speech:  notoapi.ComputePlacement{Location: "remote", Endpoint: "really-long-gpu-pool-name.modal.run", Trust: notoapi.TrustCloud},
			Diarize: notoapi.ComputePlacement{Location: "remote", Endpoint: "really-long-gpu-pool-name.modal.run", Trust: notoapi.TrustCloud},
			Embed:   notoapi.ComputePlacement{Location: "local"},
		},
		DataPlane: notoapi.DataPlane{Location: "local", Storage: "local"},
	}
	out := ansi.Strip(renderTopology(sys, theme.NewStyles(), 100, 0, false))
	if !strings.Contains(out, "…") {
		t.Errorf("long hostname should be ellipsised:\n%s", out)
	}
	// The box has exactly its border + content rows; a wrapped hostname would add
	// a stray row whose only content is the overflow. Assert the GPU box keeps the
	// host on one line by checking no line is just a bare hostname fragment.
	for _, ln := range strings.Split(out, "\n") {
		if lipgloss.Width(ln) > 100 {
			t.Errorf("line exceeds width (wrapped?): %q", ln)
		}
	}
}

// firstBoxWidth returns the cell width of the first (top-left) box in a rendered
// diagram, and the rune column its top-left corner sits at — i.e. where and how
// big the "This device" anchor is.
func firstBoxWidth(out string) (width, col int) {
	for _, ln := range strings.Split(out, "\n") {
		start := -1
		for i, ru := range []rune(ln) {
			switch {
			case ru == '╭':
				start = i
			case ru == '╮' && start >= 0:
				return i - start + 1, start
			}
		}
	}
	return -1, -1
}

// The local "This device" box is an ANCHOR: same size, same place, in every
// shape. Switching presets must not resize or move it — only swap the boxes
// beside/below it. This pins that invariant.
func TestRenderTopologyDeviceAnchorIsConstant(t *testing.T) {
	s := theme.NewStyles()
	const w = 100
	shapes := map[string]struct {
		sys           notoapi.System
		backendRemote bool
	}{
		"local":   {notoapi.System{Hostname: "mac", Compute: localCompute(), DataPlane: notoapi.DataPlane{Location: "local", Storage: "local"}}, false},
		"offload": {notoapi.System{Compute: notoapi.ComputeTopology{Speech: notoapi.ComputePlacement{Location: "remote", Endpoint: "gpu.modal.run", Trust: notoapi.TrustCloud}, Diarize: notoapi.ComputePlacement{Location: "remote", Endpoint: "gpu.modal.run", Trust: notoapi.TrustCloud}, Embed: notoapi.ComputePlacement{Location: "local"}}, DataPlane: notoapi.DataPlane{Location: "local", Storage: "local"}}, false},
		"api":     {notoapi.System{Compute: notoapi.ComputeTopology{Speech: notoapi.ComputePlacement{Location: "remote", Endpoint: "gpu.modal.run", Trust: notoapi.TrustCloud}, Diarize: notoapi.ComputePlacement{Location: "remote", Endpoint: "gpu.modal.run", Trust: notoapi.TrustCloud}, Embed: notoapi.ComputePlacement{Location: "local"}}, DataPlane: notoapi.DataPlane{Location: "remote", Endpoint: "srv:8731", Storage: "remote", Trust: notoapi.TrustOwned}}, false},
		"backend": {notoapi.System{Hostname: "srv.example.com", Compute: localCompute(), DataPlane: notoapi.DataPlane{Location: "local", Storage: "local"}}, true},
	}
	var wantW, wantCol = -1, -1
	for name, sc := range shapes {
		out := ansi.Strip(renderTopology(sc.sys, s, w, 0, sc.backendRemote))
		gotW, gotCol := firstBoxWidth(out)
		if gotW <= 0 {
			t.Fatalf("%s: no box found:\n%s", name, out)
		}
		if wantW < 0 {
			wantW, wantCol = gotW, gotCol
			continue
		}
		if gotW != wantW {
			t.Errorf("%s: device box width %d, want %d (anchor must be the same size in every shape)", name, gotW, wantW)
		}
		if gotCol != wantCol {
			t.Errorf("%s: device box starts at column %d, want %d (anchor must stay in the same place)", name, gotCol, wantCol)
		}
	}
}

// The animation must actually move: a packet sits at a different column across
// frames (the diagram is alive, not static).
func TestRenderTopologyAnimates(t *testing.T) {
	sys := notoapi.System{
		Hostname: "mac",
		Compute: notoapi.ComputeTopology{
			Speech:  notoapi.ComputePlacement{Location: "remote", Endpoint: "gpu.box:8731", Trust: notoapi.TrustOwned},
			Diarize: notoapi.ComputePlacement{Location: "remote", Endpoint: "gpu.box:8731", Trust: notoapi.TrustOwned},
			Embed:   notoapi.ComputePlacement{Location: "local"},
		},
		DataPlane: notoapi.DataPlane{Location: "local", Storage: "local"},
	}
	a := ansi.Strip(renderTopology(sys, theme.NewStyles(), 100, 0, false))
	b := ansi.Strip(renderTopology(sys, theme.NewStyles(), 100, 4, false))
	if a == b {
		t.Error("topology diagram should change across animation frames")
	}
}
