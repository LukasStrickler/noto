package tui

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/lukasstrickler/noto/internal/transport/notoapi"
	"github.com/lukasstrickler/noto/internal/ui/tui/layout"
	"github.com/lukasstrickler/noto/internal/ui/tui/theme"
)

// The deployment diagram tells the whole story of where a meeting's data goes:
// which boxes are systems the user owns (this device, their own server) vs a
// third-party cloud, and the actual two-way data flow between them — audio /
// artifacts out, transcripts / results back. Box borders are STATIC; only the
// packets travel along the connectors (driven by `frame`), so the motion is the
// data moving, not the chrome flashing. It adapts to the topology: one box when
// everything is local, two side-by-side when compute or data is offloaded, and a
// 2-D corner hub for the full split (this device top-left, GPU compute to its
// right, the data plane below).

// Node colour classes.
const (
	nodeSelf  = "self"  // this device — blue
	nodeOwned = "owned" // the user's own remote server — green
	nodeCloud = "cloud" // a third-party provider — amber
)

// nodeRamps are per-class colour shades; index [2] is the bright, steady border
// shade used by staticColor and the compact-list markers. (Borders no longer
// pulse — only the connector packets move — so the dim/bright ends are kept just
// as the source of the one steady tone.)
var nodeRamps = map[string][]string{
	nodeSelf:  {"#27374d", "#3b6fb0", "#60a5fa", "#93c5fd", "#60a5fa", "#3b6fb0"},
	nodeOwned: {"#1c4034", "#1f7a5f", "#34d399", "#6ee7b7", "#34d399", "#1f7a5f"},
	nodeCloud: {"#4a3410", "#8a6a1a", "#fbbf24", "#fde08a", "#fbbf24", "#8a6a1a"},
}

type planeNode struct {
	title string
	lines []string
	class string
}

type planeConn struct {
	fwd, back string // forward (left→right) and back (right→left) flow labels
	cloud     bool   // the forward flow leaves the user's systems
}

// Layout constants for the boxed (horizontal) mode. A box's total width is its
// content width plus 2 for the border and 2 for the 1-cell side padding.
const (
	cardMinContent = 14 // narrowest a box's interior is allowed to get
	cardMaxContent = 28 // widest — hostnames longer than this are middle-truncated
	cardChrome     = 4  // border (2) + padding (2)
	connWidth      = 14 // a horizontal connector strip is this many cells wide
)

// renderTopology draws the deployment diagram. frame advances the packet
// animation on the connectors; it is ignored for the single-node (all-local)
// case, which renders statically — there's no flow to animate when nothing
// leaves the machine. backendRemote is true when this is a thin client to a
// remote backend, which disambiguates the all-local-here case from the
// all-on-a-remote-server case.
//
// The diagram is responsive and consistent: "This device" is always the same
// size and place (the anchor), with remote boxes beside it (a 2-box row) or, for
// the full split, the data plane below it in a 2-D corner hub. A too-narrow
// window drops to a compact bordered-less list so it never overruns the panel.
func renderTopology(sys notoapi.System, s theme.Styles, width, frame int, backendRemote bool) string {
	nodes, conns := topologyGraph(sys, backendRemote)
	header := s.HeaderEm.Render("Deployment topology") + "   " +
		s.Muted.Render("shape: ") + s.Info.Render(topologyLabel(sys, backendRemote))

	// One interior width for EVERY shape — derived from the two-box row that the
	// multi-node layouts use — so the "This device" box is the same size whether
	// it stands alone (Local) or sits beside/above another box. Switching presets
	// never resizes or moves the anchor.
	cw := layout.Clamp((width-connWidth)/2-cardChrome, cardMinContent, cardMaxContent)
	fits := width >= 2*(cardMinContent+cardChrome)+connWidth

	var body string
	switch {
	case len(nodes) == 1:
		body = renderSingle(s, cw, nodes[0])
	case len(nodes) == 3 && fits:
		// The full split is a 2-D corner: this device top-left, compute to its
		// right, the data plane below — never three boxes in one over-wide row.
		body = renderHub(s, nodes, conns, cw, frame)
	case fits:
		body = renderHorizontal(s, nodes, conns, cw, frame)
	default:
		// Too narrow for boxes: the compact list keeps it short and unwrapped.
		body = renderCompact(s, nodes, conns, width, frame)
	}

	return header + "\n\n" + body + "\n\n" + legend(sys, s, width)
}

// renderSingle draws the one-box, all-local case at the shared interior width cw
// (so the device box matches its size in the multi-node shapes) with a static
// border — there's no data flow, so there's nothing to animate.
func renderSingle(s theme.Styles, cw int, n planeNode) string {
	return box(s, cw+cardChrome, padNode(n, cw), staticColor(n.class))
}

// renderHorizontal lays the nodes left→right as equal-height boxes joined at the
// top (so unequal content never staggers them), with an animated bidirectional
// connector between each pair. Every content line is truncated to the interior
// width first, so a long hostname is ellipsised rather than wrapped.
func renderHorizontal(s theme.Styles, nodes []planeNode, conns []planeConn, cw, frame int) string {
	// Equalise box heights: a box's rows = title + len(lines); pad the shorter
	// nodes with blank lines so every box (and the connector strip) is the same
	// height and JoinHorizontal(Top) lines them up cleanly.
	maxRows := 0
	for _, n := range nodes {
		if r := 1 + len(n.lines); r > maxRows {
			maxRows = r
		}
	}
	boxH := maxRows + 2 // + top & bottom border

	parts := make([]string, 0, len(nodes)*2)
	for i, n := range nodes {
		n.title = truncLine(n.title, cw)
		lines := make([]string, 0, maxRows-1)
		for _, ln := range n.lines {
			lines = append(lines, truncLine(ln, cw))
		}
		for len(lines) < maxRows-1 {
			lines = append(lines, "")
		}
		n.lines = lines
		parts = append(parts, box(s, cw+cardChrome, n, staticColor(n.class)))
		if i < len(conns) {
			parts = append(parts, hConnector(s, conns[i], frame, boxH))
		}
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, parts...)
}

// renderHub draws the full three-plane split as a 2-D corner layout instead of
// three boxes in one wide row: this device sits top-left (always nodes[0]) with
// the compute box to its right over a horizontal connector, and the data plane
// below it over a vertical connector. It reuses renderHorizontal for the top
// pair, so the two share equal-height boxing and the same animated wire; borders
// are static and only the connector packets move.
func renderHub(s theme.Styles, nodes []planeNode, conns []planeConn, cw, frame int) string {
	device, compute, data := nodes[0], nodes[1], nodes[2]
	topConn, downConn := conns[0], conns[1]

	top := renderHorizontal(s, []planeNode{device, compute}, []planeConn{topConn}, cw, frame)

	boxW := cw + cardChrome
	dataBox := box(s, boxW, padNode(data, cw), staticColor(data.class))

	indent := boxW/2 - 1
	if indent < 1 {
		indent = 1
	}
	return lipgloss.JoinVertical(lipgloss.Left, top, vConnector(s, downConn, frame, indent), dataBox)
}

// vConnector is the vertical twin of hConnector, drawn in the SAME visual
// language so the device↔data link reads like the device↔compute one, just
// rotated: two solid colour rails (left = forward/down in the flow colour, right
// = back/up in green), each carrying a single travelling packet in opposite
// directions, with a muted label + direction chevron at the matching end. Only
// the dots move — same as the horizontal connector.
//
// The rail is deliberately taller than it is wide and the packet steps one row
// every OTHER frame, so the short vertical run drifts at the same calm cadence as
// the long horizontal wire instead of darting up and down (a 4-row rail at full
// frame rate read as a hectic flicker). The two dots are mirrored, so they meet
// and align mid-rail before passing.
func vConnector(s theme.Styles, c planeConn, frame, indent int) string {
	const railH = 5
	pad := strings.Repeat(" ", indent)
	fwd := lipgloss.NewStyle().Foreground(flowColor(s, c.cloud)) // device → data (down)
	back := lipgloss.NewStyle().Foreground(s.T.Success)          // data → device (up)

	step := frame / 2 // advance one row every other frame
	downPos := ((step % railH) + railH) % railH
	upPos := railH - 1 - downPos

	rows := make([]string, railH)
	for r := 0; r < railH; r++ {
		left := fwd.Render("│")
		if r == downPos {
			left = fwd.Render("•")
		}
		right := back.Render("│")
		if r == upPos {
			right = back.Render("•")
		}
		row := pad + left + " " + right
		switch r {
		case 0:
			row += "  " + s.Muted.Render(c.fwd+" ▾")
		case railH - 1:
			row += "  " + s.Muted.Render("▴ "+c.back)
		}
		rows[r] = row
	}
	return strings.Join(rows, "\n")
}

// padNode truncates a node's title and lines to the interior width cw without
// padding its height — for a standalone box that isn't part of an equal-height row.
func padNode(n planeNode, cw int) planeNode {
	n.title = truncLine(n.title, cw)
	lines := make([]string, len(n.lines))
	for i, ln := range n.lines {
		lines[i] = truncLine(ln, cw)
	}
	n.lines = lines
	return n
}

// renderCompact is the narrow-window fallback: a borderless list of nodes with a
// coloured marker, each followed by a one-line bidirectional flow arrow. It stays
// short (no box chrome, no inter-box blanks) so three planes still fit above the
// legend at the panel's minimum height.
func renderCompact(s theme.Styles, nodes []planeNode, conns []planeConn, width, frame int) string {
	var out []string
	for i, n := range nodes {
		marker, mc := markerFor(n.class)
		out = append(out, lipgloss.NewStyle().Foreground(mc).Render(marker)+" "+
			s.HeaderEm.Render(truncLine(n.title, width-2)))
		for _, ln := range n.lines {
			out = append(out, "   "+s.Muted.Render(truncLine(ln, width-3)))
		}
		if i < len(conns) {
			out = append(out, compactConn(s, conns[i], frame, width))
		}
	}
	return strings.Join(out, "\n")
}

// box renders a single rounded-border card at total width w with the given
// border colour. Content is assumed already truncated to fit.
func box(s theme.Styles, w int, n planeNode, border color.Color) string {
	content := s.HeaderEm.Render(n.title)
	if len(n.lines) > 0 {
		content += "\n" + strings.Join(n.lines, "\n")
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(border).
		Padding(0, 1).
		Width(w).
		Render(content)
}

// hConnector is the animated bidirectional strip between two horizontal boxes.
// The two wire rows (top → forward, bottom → back) run the full width so they
// touch both box borders and read as a continuous wire; the label rows are inset
// by a leading space so they never glue to a border. The four content rows are
// centred in a strip of the box's height so they meet the boxes mid-card.
func hConnector(s theme.Styles, c planeConn, frame, height int) string {
	rows := make([]string, height)
	blank := strings.Repeat(" ", connWidth)
	for i := range rows {
		rows[i] = blank
	}
	start := (height - 4) / 2
	if start < 0 {
		start = 0
	}
	place := func(off int, line string) {
		if y := start + off; y >= 0 && y < height {
			rows[y] = line
		}
	}
	place(0, s.Muted.Render(padLabel(" "+c.fwd+" ▸", connWidth)))
	place(1, packetLine(connWidth, frame%connWidth, flowColor(s, c.cloud)))
	place(2, packetLine(connWidth, connWidth-1-(frame%connWidth), s.T.Success))
	place(3, s.Muted.Render(padLabel(" ◂ "+c.back, connWidth)))
	return strings.Join(rows, "\n")
}

// compactConn is the one-line connector used by the narrow-window fallback: a
// short rail with a single travelling packet (the "point" that keeps the list
// alive) and the forward/back flow labels. Only the dot moves; the colours are
// steady, matching the static box borders.
func compactConn(s theme.Styles, c planeConn, frame, width int) string {
	const railW = 6
	rail := packetLine(railW, frame%railW, flowColor(s, c.cloud))
	label := s.Muted.Render(" " + c.fwd + " ▸   ◂ " + c.back)
	return truncLine("   "+rail+label, width)
}

func packetLine(w, pos int, c color.Color) string {
	r := []rune(strings.Repeat("─", w))
	if pos >= 0 && pos < w {
		r[pos] = '•'
	}
	return lipgloss.NewStyle().Foreground(c).Render(string(r))
}

func flowColor(s theme.Styles, cloud bool) color.Color {
	if cloud {
		return s.T.Warning
	}
	return s.T.Info
}

// markerFor returns the compact-list bullet and its colour for a node class.
func markerFor(class string) (string, color.Color) {
	switch class {
	case nodeCloud:
		return "○", lipgloss.Color(nodeRamps[nodeCloud][2])
	case nodeOwned:
		return "●", lipgloss.Color(nodeRamps[nodeOwned][2])
	default:
		return "◆", lipgloss.Color(nodeRamps[nodeSelf][2])
	}
}

// staticColor is the steady border shade for a class — the bright middle of its
// ramp. Every box border uses this: the chrome holds still and only the packets
// on the connectors animate.
func staticColor(class string) color.Color {
	ramp := nodeRamps[class]
	if len(ramp) == 0 {
		ramp = nodeRamps[nodeSelf]
	}
	return lipgloss.Color(ramp[2])
}

// padLabel right-pads a label to w display cells (cell-aware, so the multibyte
// flow arrows don't get mis-measured the way a byte-length pad would).
func padLabel(s string, w int) string {
	if cw := lipgloss.Width(s); cw < w {
		return s + strings.Repeat(" ", w-cw)
	}
	return s
}

// truncLine fits a line to max display cells without ever wrapping. Hostnames
// (a dotted token with no spaces) keep their head and tail via a middle ellipsis
// — the leading subdomain and the trailing port/TLD are the identifying parts;
// everything else is tail-truncated.
func truncLine(s string, max int) string {
	if max <= 0 || lipgloss.Width(s) <= max {
		if max <= 0 {
			return ""
		}
		return s
	}
	if looksLikeHost(s) {
		return truncMiddle(s, max)
	}
	return truncTail(s, max)
}

func truncTail(s string, max int) string {
	if max <= 1 {
		return "…"
	}
	r := []rune(s)
	out := string(r[:0])
	w := 0
	for _, ru := range r {
		cw := lipgloss.Width(string(ru))
		if w+cw > max-1 {
			break
		}
		out += string(ru)
		w += cw
	}
	return out + "…"
}

func truncMiddle(s string, max int) string {
	if max <= 1 {
		return "…"
	}
	r := []rune(s)
	keep := max - 1 // room taken by the ellipsis
	head := (keep + 1) / 2
	tail := keep - head
	out := string(r[:head]) + "…" + string(r[len(r)-tail:])
	for lipgloss.Width(out) > max && head > 0 {
		head--
		out = string(r[:head]) + "…" + string(r[len(r)-tail:])
	}
	return out
}

func looksLikeHost(s string) bool {
	return strings.Contains(s, ".") && !strings.Contains(s, " ")
}

// topologyGraph turns a System payload into the nodes + connections to draw.
//
// "This device" is ALWAYS nodes[0] and every node is the same size (title + 3
// lines), so the local box stays anchored at the same place and size across all
// shapes — toggling presets swaps the remote box(es) in beside/below it rather
// than reshuffling everything. The shapes mirror the canonical config presets
// (config.PresetLabel): Local, Offload compute, API server, Remote backend, and
// the unnamed "data only" Custom.
func topologyGraph(sys notoapi.System, backendRemote bool) ([]planeNode, []planeConn) {
	speechRemote := sys.Compute.Speech.Location == "remote"
	diarRemote := sys.Compute.Diarize.Location == "remote"
	computeRemote := speechRemote || diarRemote
	dataRemote := sys.DataPlane.Location == "remote"

	// Thin client to a remote backend: capture is local (edge capture); the
	// server does everything else. This is the "local TUI + remote server" shape
	// that the System payload alone can't distinguish from all-local.
	if backendRemote {
		return []planeNode{
				deviceNode("TUI · capture", "edge capture", "thin client"),
				serverNode(sys, computeRemote),
			},
			[]planeConn{{fwd: "audio", back: "results", cloud: false}}
	}

	switch {
	case !computeRemote && !dataRemote: // Local — one box, nothing leaves
		return []planeNode{deviceNode("TUI · capture", "compute · storage", "identity ●")}, nil

	case computeRemote && !dataRemote: // Offload compute — GPU to the right
		return []planeNode{
				deviceNode("TUI · capture", "identity ● · storage", "orchestrates"),
				computeNode(sys),
			},
			[]planeConn{{fwd: "audio", back: "transcript", cloud: cloudCompute(sys)}}

	case !computeRemote && dataRemote: // Custom: data off-site, compute local
		return []planeNode{
				deviceNode("TUI · capture", "compute · identity ●", "orchestrates"),
				dataNode(sys),
			},
			[]planeConn{{fwd: "artifacts", back: "results", cloud: false}}

	default: // API server — the full split: GPU to the right, data below
		return []planeNode{
				deviceNode("TUI · capture", "identity ●", "orchestrates"),
				computeNode(sys),
				dataNode(sys),
			},
			[]planeConn{
				{fwd: "audio", back: "transcript", cloud: cloudCompute(sys)}, // device ↔ compute (top, horizontal)
				{fwd: "artifacts", back: "results", cloud: false},            // device ↔ data (below, vertical)
			}
	}
}

// deviceNode is the canonical "This device" box, padded to exactly 3 lines so it
// is the same size in every shape. Only the role lines differ; title, class, and
// height are constant, which is what keeps the local anchor from moving.
func deviceNode(lines ...string) planeNode {
	out := make([]string, 3)
	copy(out, lines)
	return planeNode{title: "This device", class: nodeSelf, lines: out}
}

func computeNode(sys notoapi.System) planeNode {
	host, trust := firstRemoteCompute(sys.Compute)
	class, owner := nodeOwned, "your server ●"
	if trust == notoapi.TrustCloud {
		class, owner = nodeCloud, "third-party ○"
	}
	return planeNode{title: "Compute · GPU", class: class, lines: []string{"STT + diarization", host, owner}}
}

func dataNode(sys notoapi.System) planeNode {
	host := sys.DataPlane.Endpoint
	if host == "" {
		host = "this machine"
	}
	class, owner := nodeOwned, "your server ●"
	if sys.DataPlane.Trust == notoapi.TrustCloud {
		class, owner = nodeCloud, "third-party ○"
	}
	return planeNode{title: "Data + API", class: class, lines: []string{host, "storage + agent API", owner}}
}

// serverNode is the user's own backend in the thin-client shape — always owned
// (green), three lines like every other box.
func serverNode(sys notoapi.System, computeRemote bool) planeNode {
	host := sys.Hostname
	if host == "" {
		host = "your server"
	}
	compute := "compute (local)"
	if computeRemote {
		cHost, _ := firstRemoteCompute(sys.Compute)
		compute = "compute → " + cHost
	}
	return planeNode{title: "Backend server", class: nodeOwned, lines: []string{host, compute, "storage · agent API"}}
}

func cloudCompute(sys notoapi.System) bool {
	_, trust := firstRemoteCompute(sys.Compute)
	return trust == notoapi.TrustCloud
}

func firstRemoteCompute(c notoapi.ComputeTopology) (host, trust string) {
	for _, p := range []notoapi.ComputePlacement{c.Speech, c.Diarize, c.Embed} {
		if p.Location == "remote" {
			return p.Endpoint, p.Trust
		}
	}
	return "", ""
}

// legend explains the colour code and the data flow, and warns when audio leaves
// the user's systems to a third party. Chips wrap to the available width so the
// key never overruns the panel; the warning's hostname is truncated to fit.
func legend(sys notoapi.System, s theme.Styles, width int) string {
	owned := lipgloss.NewStyle().Foreground(s.T.Success).Render("● yours")
	cloud := lipgloss.NewStyle().Foreground(s.T.Warning).Render("○ cloud (third-party)")
	lines := []string{
		packChips(width, owned, cloud),
		packChips(width,
			s.Muted.Render("▸ audio/data out"),
			s.Muted.Render("◂ results back"),
			s.Muted.Render("● identity stays on-device")),
	}
	if host, trust := firstRemoteCompute(sys.Compute); trust == notoapi.TrustCloud {
		lines = append(lines, lipgloss.NewStyle().Foreground(s.T.Warning).Render(warnLine(host, width)))
	}
	return strings.Join(lines, "\n")
}

// packChips greedily packs styled chips onto lines (3-space gutter), wrapping
// when the next chip would exceed width. Width is measured in cells so the
// styled (ANSI-wrapped) chips pack correctly.
func packChips(width int, chips ...string) string {
	const gap = "   "
	var lines []string
	cur := ""
	for _, c := range chips {
		switch {
		case cur == "":
			cur = c
		case lipgloss.Width(cur)+lipgloss.Width(gap)+lipgloss.Width(c) <= width:
			cur += gap + c
		default:
			lines = append(lines, cur)
			cur = c
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return strings.Join(lines, "\n")
}

// warnLine builds the "audio leaves your systems" warning, keeping the hostname
// readable by middle-truncating it (and dropping to a terse prefix) when the
// full sentence won't fit.
func warnLine(host string, width int) string {
	prefix := "⚠ audio leaves your systems → "
	if lipgloss.Width(prefix)+lipgloss.Width(host) > width {
		if width-lipgloss.Width(prefix) < 8 {
			prefix = "⚠ → "
		}
		host = truncMiddle(host, max(1, width-lipgloss.Width(prefix)))
	}
	return prefix + host
}

// topologyLabel infers a human shape label from a System payload, mirroring
// config.DetectPreset / config.PresetLabel over the wire DTO — same names, same
// order, so the diagram's label matches the preset the user would pick. A
// data-only ("store off-site") shape has no named preset, so it reads "Custom".
func topologyLabel(sys notoapi.System, backendRemote bool) string {
	speechRemote := sys.Compute.Speech.Location == "remote"
	diarRemote := sys.Compute.Diarize.Location == "remote"
	embedRemote := sys.Compute.Embed.Location == "remote"
	dataRemote := sys.DataPlane.Location == "remote"
	switch {
	case backendRemote:
		return "Remote backend"
	case dataRemote && speechRemote && diarRemote && !embedRemote:
		return "API server"
	case speechRemote && diarRemote && !embedRemote:
		return "Offload compute"
	case !speechRemote && !diarRemote && !embedRemote && !dataRemote:
		return "Local"
	default:
		return "Custom"
	}
}
