package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/lukasstrickler/noto/internal/transport/notoapi"
	"github.com/lukasstrickler/noto/internal/ui/tui/hit"
	"github.com/lukasstrickler/noto/internal/ui/tui/layout"
	"github.com/lukasstrickler/noto/internal/ui/tui/theme"
)

// configScreen is the unified configuration surface (screen 3). Left
// pane is a section nav; right pane shows the selected section's
// content + actions. Tab/Shift-Tab moves focus between the two panes.
//
// Sections are intentionally small and orthogonal:
//   - Active routes  → which speech / LLM provider is in use
//   - API keys       → manage credentials for cloud providers
//   - Storage        → recordings dir, index state, retention
//   - Paths          → read-only filesystem paths
//
// Replaces the older standalone `providers` / `settings` / `storage`
// screens (formerly screens 4/5/6). All three entry keys now collapse
// to this one surface.
type configScreen struct {
	section    int // index into sections[]
	rightFocus bool

	cfg     notoapi.Config
	cfgLoad bool

	provs    []notoapi.ProviderInfo
	provLoad bool

	storage     notoapi.Storage
	storageLoad bool

	sys     notoapi.System
	sysLoad bool

	topoFrame     int  // deployment-diagram animation frame
	topoAnim      bool // true while the animation tick is scheduled
	backendRemote bool // true when connected to a remote backend (thin client)

	// cursors per-section (active routes, api keys, storage, paths, deployment)
	cursors [5]int

	keyEdit  bool
	editID   string
	keyInput textinput.Model

	err error
}

const (
	secActive = iota
	secAPIKeys
	secStorage
	secPaths
	secDeployment
)

type configSection struct {
	id    int
	label string
	hint  string
}

func (c *configScreen) sections() []configSection {
	return []configSection{
		{secActive, "Active routes", "speech + LLM"},
		{secAPIKeys, "API keys", fmt.Sprintf("%d cloud providers", c.countKeyProviders())},
		{secStorage, "Storage", "health · retention"},
		{secPaths, "Paths", "where data lives"},
		{secDeployment, "Deployment", "where compute + data run"},
	}
}

func (c *configScreen) countKeyProviders() int {
	n := 0
	for _, p := range c.provs {
		if p.Kind == "fake" {
			continue
		}
		n++
	}
	return n
}

// --- attention: route the nav-pill flag down to the exact broken thing ------
//
// The Config nav pill flags the page; these carry the SAME signal down so a
// user who lands on Config sees where to act without hunting: the section nav
// badges "API keys ⚑N", the Active-routes section marks the broken route, and
// the offending provider row reads "⚑ add key" with the amber bar beside it.
// All of it counts only the ACTIVE route missing a key, so the trail matches
// the nav count (ConfigIssues / countConfigIssues on the service).

// providerNeedsKey reports whether a provider is an attention item: it's the
// active speech/LLM route, takes an API key, and has none.
func (c *configScreen) providerNeedsKey(p notoapi.ProviderInfo) bool {
	// Only providers that actually require a credential can be "missing a key".
	// The local default (Parakeet) and fakes require none, so they never nag.
	return p.RequiresKey && (p.IsActiveSpeech || p.IsActiveLLM) && !p.HasKey
}

// keyIssueCount is how many active routes are missing their key.
func (c *configScreen) keyIssueCount() int {
	n := 0
	for _, p := range c.provs {
		if c.providerNeedsKey(p) {
			n++
		}
	}
	return n
}

// sectionAttention is the per-section flag count for the left nav, so the
// section that owns the work is badged and the user knows where to go.
func (c *configScreen) sectionAttention(id int) int {
	if id == secAPIKeys {
		return c.keyIssueCount()
	}
	return 0
}

// activeProviderForKind returns the provider currently routing the given kind
// ("speech"/"llm"), so the Active-routes section can flag a broken route.
func (c *configScreen) activeProviderForKind(kind string) (notoapi.ProviderInfo, bool) {
	for _, p := range c.provs {
		if (kind == "speech" && p.IsActiveSpeech) || (kind == "llm" && p.IsActiveLLM) {
			return p, true
		}
	}
	return notoapi.ProviderInfo{}, false
}

func newConfigScreen() screen {
	ti := textinput.New()
	ti.EchoMode = textinput.EchoPassword
	ti.EchoCharacter = '•'
	ti.CharLimit = 256
	ti.Placeholder = "paste API key…"
	return &configScreen{
		cfgLoad:     true,
		provLoad:    true,
		storageLoad: true,
		keyInput:    ti,
	}
}

func (c *configScreen) id() screenID      { return sConfig }
func (c *configScreen) title() string     { return "config" }
func (c *configScreen) inputActive() bool { return c.keyEdit }

func (c *configScreen) enter(ctx screenCtx, _ string) tea.Cmd {
	// Whether we're a thin client to a remote backend — disambiguates the
	// otherwise-identical "all local" vs "remote server doing everything" cases
	// in the deployment diagram (the System payload describes the backend, which
	// can't know the client reached it over the network).
	if r, ok := ctx.client.(interface{ IsRemote() bool }); ok {
		c.backendRemote = r.IsRemote()
	}
	cmds := []tea.Cmd{
		fetchConfig(ctx),
		fetchProviders(ctx),
		fetchStorage(ctx),
		fetchSystem(ctx),
	}
	// Start the deployment-diagram animation. The guard prevents a second tick
	// chain if enter fires while one is already alive.
	if !c.topoAnim {
		c.topoAnim = true
		cmds = append(cmds, topoTickCmd())
	}
	return tea.Batch(cmds...)
}

// leave stops the animation: the in-flight tick is routed to the next screen
// (which ignores it), so the chain dies; clearing the flag lets enter restart it.
func (c *configScreen) leave(_ screenCtx) tea.Cmd {
	c.topoAnim = false
	return nil
}

// animates reports whether the currently-previewed deployment shape has data
// flowing between machines — i.e. more than one node to draw. A single-node
// (all-local) diagram is static, so the animation tick is pointless and stops.
func (c *configScreen) animates() bool {
	sys, backend := c.previewSystem()
	nodes, _ := topologyGraph(sys, backend)
	return len(nodes) > 1
}

// maybeStartAnim (re)starts the deployment-diagram tick when the previewed shape
// animates and no tick is in flight. Called whenever the preview selection or the
// live system changes, so cycling to a multi-node preset brings it to life.
func (c *configScreen) maybeStartAnim() tea.Cmd {
	if c.animates() && !c.topoAnim {
		c.topoAnim = true
		return topoTickCmd()
	}
	return nil
}

// topoPreview is one selectable deployment shape in the Deployment section.
// Every shape is a synthetic example so a user on a plain local setup can still
// see what each topology looks like; the one that matches THIS install is flagged
// live and rendered with the install's real endpoints.
type topoPreview struct {
	label, desc   string
	sys           notoapi.System
	backendRemote bool
	live          bool // matches the shape this install actually runs
}

// topoPreviews is the list the Deployment section lets you cycle through, ordered
// simplest → most involved (Local · Offload compute · Remote backend · API
// server) so scrolling down walks from "all on this device" to the full split.
// The shape matching the live install is flagged (and shows its real endpoints).
func (c *configScreen) topoPreviews() []topoPreview {
	modal := func(ep string) notoapi.ComputePlacement {
		return notoapi.ComputePlacement{Location: "remote", Endpoint: ep, Trust: notoapi.TrustCloud}
	}
	localAll := notoapi.ComputeTopology{
		Speech:  notoapi.ComputePlacement{Location: "local"},
		Diarize: notoapi.ComputePlacement{Location: "local"},
		Embed:   notoapi.ComputePlacement{Location: "local"},
	}
	const gpu = "noto--gpu.modal.run"
	const srv = "noto.your-server.example.com:8731"
	localData := notoapi.DataPlane{Location: "local", Storage: "local"}
	cloudGPU := notoapi.ComputeTopology{Speech: modal(gpu), Diarize: modal(gpu), Embed: notoapi.ComputePlacement{Location: "local"}}

	previews := []topoPreview{
		{label: "Local", desc: "everything on this device — nothing leaves",
			sys: notoapi.System{Hostname: "this-mac", Accelerator: "coreml", Compute: localAll, DataPlane: localData}},
		{label: "Offload compute", desc: "speech on a cloud GPU; your data stays local",
			sys: notoapi.System{Hostname: "this-mac", Compute: cloudGPU, DataPlane: localData}},
		{label: "Remote backend", desc: "thin client to your server; only capture is local",
			sys: notoapi.System{Hostname: srv, Compute: localAll, DataPlane: localData}, backendRemote: true},
		{label: "API server", desc: "cloud GPU for speech, your server for data + API",
			sys: notoapi.System{Compute: cloudGPU,
				DataPlane: notoapi.DataPlane{Location: "remote", Endpoint: srv, Storage: "remote", Trust: notoapi.TrustOwned}}},
	}
	// Flag the shape this install runs and swap in its real endpoints, so the
	// "current" row shows your actual hosts rather than the synthetic example.
	live := topologyLabel(c.sys, c.backendRemote)
	for i := range previews {
		if previews[i].label == live {
			previews[i].live = true
			previews[i].sys = c.sys
			previews[i].backendRemote = c.backendRemote
		}
	}
	return previews
}

// livePreviewIndex is the index of the preview matching the live install (or 0 if
// the install is a Custom shape with no named preset).
func (c *configScreen) livePreviewIndex() int {
	for i, p := range c.topoPreviews() {
		if p.live {
			return i
		}
	}
	return 0
}

// previewSystem returns the System + backend-remote bit for the selected preview,
// which drives both the rendered diagram and animates().
func (c *configScreen) previewSystem() (notoapi.System, bool) {
	p := c.topoPreviews()
	i := layout.Clamp(c.cursors[secDeployment], 0, len(p)-1)
	return p[i].sys, p[i].backendRemote
}

func (c *configScreen) update(ctx screenCtx, msg tea.Msg) (screen, tea.Cmd) {
	switch v := msg.(type) {
	case configLoadedMsg:
		c.cfgLoad = false
		c.cfg = v.Cfg
		if v.Err != nil {
			c.err = v.Err
		}
	case providersLoadedMsg:
		c.provLoad = false
		c.provs = v.Providers
		if v.Err != nil {
			c.err = v.Err
		}
	case storageLoadedMsg:
		c.storageLoad = false
		c.storage = v.Storage
	case systemLoadedMsg:
		c.sysLoad = false
		c.sys = v.System
		if v.Err != nil {
			c.err = v.Err
		}
		// Open the preview list on the shape this install actually runs, so the
		// section lands on "your setup" rather than always at Local.
		c.cursors[secDeployment] = c.livePreviewIndex()
		// The topology was unknown when enter() fired, so the tick chain may have
		// already stopped (single-node default). Restart it now if the loaded
		// shape (or the selected preview of it) actually animates.
		return c, c.maybeStartAnim()
	case topoTickMsg:
		c.topoFrame++
		// Keep the chain alive only while there's flow to animate; a single-node
		// (all-local) diagram is static, so the tick ends and stops re-rendering.
		if c.animates() {
			return c, topoTickCmd()
		}
		c.topoAnim = false
	case providerKeyResultMsg:
		c.keyEdit = false
		c.keyInput.SetValue("")
		c.keyInput.Blur()
		if v.Err != nil {
			return c, func() tea.Msg { return bannerMsg{Kind: "error", Text: v.Err.Error()} }
		}
		return c, tea.Batch(fetchProviders(ctx), func() tea.Msg {
			return bannerMsg{Kind: "info", Text: "key saved for " + v.ProviderID}
		})
	case providerTestMsg:
		text := fmt.Sprintf("%s: %s (%dms)", v.ProviderID,
			ternary(v.Result.OK, "ok", "failed"),
			v.Result.LatencyMS)
		return c, func() tea.Msg { return bannerMsg{Kind: "info", Text: text} }
	case tea.KeyPressMsg:
		if c.keyEdit {
			return c.updateKeyInput(ctx, v)
		}
		return c.updateKey(ctx, v)
	}
	return c, nil
}

func (c *configScreen) updateKeyInput(ctx screenCtx, v tea.KeyPressMsg) (screen, tea.Cmd) {
	switch v.String() {
	case "esc":
		c.keyEdit = false
		c.keyInput.SetValue("")
		c.keyInput.Blur()
		return c, nil
	case "enter":
		val := strings.TrimSpace(c.keyInput.Value())
		if val == "" {
			return c, nil
		}
		return c, setProviderKeyCmd(ctx, c.editID, val)
	}
	var cmd tea.Cmd
	c.keyInput, cmd = c.keyInput.Update(v)
	return c, cmd
}

func (c *configScreen) updateKey(ctx screenCtx, v tea.KeyPressMsg) (screen, tea.Cmd) {
	// Left/right + tab toggle pane focus. Up/down move within the
	// focused pane.
	switch {
	case key.Matches(v, ctx.keys.Tab), key.Matches(v, ctx.keys.ShiftTab),
		key.Matches(v, ctx.keys.Left), key.Matches(v, ctx.keys.Right):
		c.rightFocus = !c.rightFocus
		return c, nil
	case key.Matches(v, ctx.keys.Up):
		c.moveCursor(-1)
		// Cycling the Deployment preview can land on an animated shape, so kick the
		// tick if it isn't already running.
		return c, c.maybeStartAnim()
	case key.Matches(v, ctx.keys.Down):
		c.moveCursor(1)
		return c, c.maybeStartAnim()
	}
	// Section-specific actions only fire when the right pane has focus.
	if !c.rightFocus {
		return c, nil
	}
	switch c.sections()[c.section].id {
	case secActive:
		return c.handleActiveKey(ctx, v)
	case secAPIKeys:
		return c.handleAPIKeyKey(ctx, v)
	}
	return c, nil
}

// --- mouse helpers (click twins of the keyboard nav) ---

// selectSection picks a left-nav section and parks focus on the nav, like
// Up/Down then Tab-back would — the click lands you on the section, ready to
// move right.
func (c *configScreen) selectSection(i int) {
	if i < 0 || i >= len(c.sections()) {
		return
	}
	c.section = i
	c.rightFocus = false
}

// focusContentRow focuses the right pane on content row i of the given section —
// clicking a route/key row both crosses into the pane and selects the row.
func (c *configScreen) focusContentRow(sectionID, i int) {
	c.rightFocus = true
	c.cursors[sectionID] = i
}

// actClick is the onClick for a right-pane action chip: the chip's key only
// fires when the right pane has focus, so a click focuses it first, then
// replays the key. Defined once so every config action chip behaves the same.
func (c *configScreen) actClick(b key.Binding) clickAction {
	return func() tea.Cmd {
		c.rightFocus = true
		if r := replayKey(b); r != nil {
			return r()
		}
		return nil
	}
}

func (c *configScreen) moveCursor(delta int) {
	if !c.rightFocus {
		n := len(c.sections())
		c.section = layout.Clamp(c.section+delta, 0, n-1)
		return
	}
	id := c.sections()[c.section].id
	var n int
	switch id {
	case secActive:
		n = len(c.activeRouteRows())
	case secAPIKeys:
		n = len(c.keyProviders())
	case secDeployment:
		n = len(c.topoPreviews())
	}
	if n == 0 {
		return
	}
	c.cursors[id] = layout.Clamp(c.cursors[id]+delta, 0, n-1)
}

// --- Active routes ---

type routeRow struct {
	kind  string // "speech" | "llm"
	label string
}

func (c *configScreen) activeRouteRows() []routeRow {
	return []routeRow{
		{"speech", "Active speech provider"},
		{"llm", "Active LLM provider"},
	}
}

func (c *configScreen) handleActiveKey(ctx screenCtx, v tea.KeyPressMsg) (screen, tea.Cmd) {
	if !key.Matches(v, ctx.keys.Enter) {
		return c, nil
	}
	// Cycle through eligible providers for the focused route kind.
	row := c.activeRouteRows()[c.cursors[secActive]]
	current := ""
	if row.kind == "speech" {
		current = c.cfg.Routing.SpeechProvider
	} else {
		current = c.cfg.Routing.LLMProvider
	}
	eligible := c.eligibleForKind(row.kind)
	if len(eligible) == 0 {
		return c, func() tea.Msg { return bannerMsg{Kind: "warn", Text: "no eligible providers"} }
	}
	next := eligible[0].ID
	for i, p := range eligible {
		if p.ID == current {
			next = eligible[(i+1)%len(eligible)].ID
			break
		}
	}
	chosen := next
	return c, func() tea.Msg {
		var err error
		if row.kind == "speech" {
			err = ctx.client.SetActiveSpeech(ctx.ctx, chosen)
		} else {
			model := ""
			for _, p := range eligible {
				if p.ID == chosen && len(p.Models) > 0 {
					model = p.Models[0].ID
					break
				}
			}
			err = ctx.client.SetActiveLLMModel(ctx.ctx, model)
		}
		if err != nil {
			return bannerMsg{Kind: "error", Text: err.Error()}
		}
		// Refetch config so the screen reflects the change.
		return tea.Batch(fetchConfig(ctx), func() tea.Msg {
			return bannerMsg{Kind: "info", Text: row.kind + " → " + chosen}
		})()
	}
}

func (c *configScreen) eligibleForKind(kind string) []notoapi.ProviderInfo {
	out := []notoapi.ProviderInfo{}
	for _, p := range c.provs {
		switch kind {
		case "speech":
			if p.Kind == "speech" {
				out = append(out, p)
			}
		case "llm":
			if p.Kind == "llm" {
				out = append(out, p)
			}
		}
	}
	return out
}

// --- API keys ---

// keyProviders returns providers that actually take an API key. Fake
// is excluded by construction so it never appears on this screen.
func (c *configScreen) keyProviders() []notoapi.ProviderInfo {
	out := []notoapi.ProviderInfo{}
	for _, p := range c.provs {
		if p.Kind == "fake" {
			continue
		}
		out = append(out, p)
	}
	return out
}

func (c *configScreen) handleAPIKeyKey(ctx screenCtx, v tea.KeyPressMsg) (screen, tea.Cmd) {
	rows := c.keyProviders()
	if len(rows) == 0 {
		return c, nil
	}
	cur := c.cursors[secAPIKeys]
	if cur >= len(rows) {
		return c, nil
	}
	pr := rows[cur]
	switch {
	case key.Matches(v, ctx.keys.Edit, ctx.keys.Enter):
		c.editID = pr.ID
		c.keyEdit = true
		c.keyInput.Focus()
	case key.Matches(v, ctx.keys.Test):
		return c, testProviderCmd(ctx, pr.ID)
	case key.Matches(v, ctx.keys.Remove):
		return c, func() tea.Msg {
			if err := ctx.client.DeleteProviderKey(ctx.ctx, pr.ID); err != nil {
				return bannerMsg{Kind: "error", Text: err.Error()}
			}
			return bannerMsg{Kind: "info", Text: "key removed for " + pr.ID}
		}
	}
	return c, nil
}

// --- View ---

func (c *configScreen) view(ctx screenCtx) string {
	s := ctx.styles
	if c.cfgLoad && c.provLoad {
		return panelEmpty(ctx, "config", s.Muted.Render("loading…"))
	}

	bp := layout.BreakpointFor(ctx.width)
	if bp == layout.Narrow {
		leftP := layout.Panel{
			Title: "config", Subtitle: "tab toggles pane",
			Width: ctx.width, Height: ctx.height, Focused: true,
		}
		ox, oy := leftP.BodyOffset()
		leftBody := c.renderLeft(ctx, ctx.width-6, ox, ctx.bodyTop+oy)
		rightY := ctx.bodyTop + oy + lipgloss.Height(leftBody) + 2
		rightBody := c.renderRight(ctx, ctx.width-6, ox, rightY)
		leftP.Body = leftBody + "\n\n" + rightBody + c.renderKeyOverlay(s)
		return leftP.Render(s)
	}
	leftW, rightW := layout.SidebarSplit(ctx.width, sidebarPref(ctx), minSidebarW, minContentW)
	leftP := layout.Panel{
		Title: "sections", Subtitle: "↑↓ + tab",
		Width: leftW, Height: ctx.height, Focused: !c.rightFocus,
	}
	lox, loy := leftP.BodyOffset()
	leftP.Body = c.renderLeft(ctx, leftW-4, lox, ctx.bodyTop+loy)
	left := leftP.Render(s)
	rightP := layout.Panel{
		Title: c.sections()[c.section].label, Subtitle: c.sections()[c.section].hint,
		Width: rightW, Height: ctx.height, Focused: c.rightFocus,
	}
	rox, roy := rightP.BodyOffset()
	rightP.Body = c.renderRight(ctx, rightW-4, (leftW+1)+rox, ctx.bodyTop+roy) + c.renderKeyOverlay(s)
	right := rightP.Render(s)
	return joinSidebar(ctx, left, right, leftW, ctx.height)
}

// renderLeft draws the section nav. originX/originY locate the body so each
// section row registers a click (select it, park focus on the nav) and lights
// up on hover. Skipped while the key-edit overlay owns input.
func (c *configScreen) renderLeft(ctx screenCtx, width, originX, originY int) string {
	s, k := ctx.styles, ctx.keys
	clickable := !c.inputActive()
	rows := []string{s.HeaderEm.Render("Configuration"), ""}
	for i, sec := range c.sections() {
		// Badge the section that owns work in its hint column: when a section
		// needs you, "⚑N" matters more than its blurb and fits the same width,
		// so the row never wraps or shifts the rows below it.
		hintCell := s.Muted.Render(fit(sec.hint, max(8, width-22)))
		if n := c.sectionAttention(sec.id); n > 0 {
			hintCell = attnCount(s, n)
		}
		row := fmt.Sprintf("%-18s %s", sec.label, hintCell)
		if i == c.section {
			marker := " ▸ "
			if !c.rightFocus {
				row = s.RowSelected.Render(marker + row)
			} else {
				row = s.HeaderEm.Render(marker) + row
			}
		} else {
			row = "   " + row
		}
		line := clipLine(row, width)
		if clickable {
			i := i
			id := fmt.Sprintf("config:sec:%d", i)
			line = rowFeedback(line, ctx.pointer().state(id, i == c.section && !c.rightFocus), s, width, 0)
			ctx.hits.Add(hit.Rect{X: originX, Y: originY + len(rows), W: width, H: 1},
				region{id: id, onClick: func() tea.Cmd { c.selectSection(i); return nil }})
		}
		rows = append(rows, line)
	}
	rows = append(rows,
		"",
		s.Muted.Render(k.Left.Help().Key+"/"+k.Right.Help().Key+" or "+k.Tab.Help().Key),
		s.Muted.Render("toggle pane focus"),
	)
	return strings.Join(rows, "\n")
}

func (c *configScreen) renderRight(ctx screenCtx, width, originX, originY int) string {
	s := ctx.styles
	switch c.sections()[c.section].id {
	case secActive:
		return c.renderActive(ctx, width, originX, originY)
	case secAPIKeys:
		return c.renderAPIKeys(ctx, width, originX, originY)
	case secStorage:
		return c.renderStorage(s, width)
	case secPaths:
		return c.renderPaths(s, width)
	case secDeployment:
		return c.renderDeployment(ctx, width, originX, originY)
	}
	return ""
}

func (c *configScreen) renderActive(ctx screenCtx, width, originX, originY int) string {
	s, k := ctx.styles, ctx.keys
	clickable := !c.inputActive()
	ptr := pointer{}
	if clickable {
		ptr = ctx.pointer()
	}
	rows := []string{
		s.HeaderEm.Render("What's actively routing your audio + LLM work"),
		"",
	}
	cur := c.cursors[secActive]
	for i, r := range c.activeRouteRows() {
		var val string
		if r.kind == "speech" {
			val = c.cfg.Routing.SpeechProvider
		} else {
			val = c.cfg.Routing.LLMProvider
			if c.cfg.Routing.LLMModel != "" {
				val += " / " + c.cfg.Routing.LLMModel
			}
		}
		// Flag the exact route whose provider has no key — the specific broken
		// thing, not just "something in config". The section badge says where to
		// fix it (API keys); this says which route is down. Reserve the flag's
		// width out of the value cell so the row never wraps or overflows.
		flag := ""
		valW := max(10, width-30)
		if p, ok := c.activeProviderForKind(r.kind); ok && c.providerNeedsKey(p) {
			flag = "  " + s.BadgeWarn.Render("⚑ no key")
			valW = max(10, valW-lipgloss.Width(flag))
		}
		line := fmt.Sprintf("  %-26s %s",
			s.Muted.Render(r.label), s.HeaderEm.Render(fit(val, valW)))
		if c.rightFocus && i == cur {
			line = s.RowSelected.Render(" ▸ " + r.label + "  " + fit(val, valW))
		}
		full := line + flag
		if clickable {
			i := i
			id := fmt.Sprintf("config:route:%d", i)
			full = rowFeedback(full, ptr.state(id, c.rightFocus && i == cur), s, width, 0)
			ctx.hits.Add(hit.Rect{X: originX, Y: originY + len(rows), W: width, H: 1},
				region{id: id, onClick: func() tea.Cmd { c.focusContentRow(secActive, i); return nil }})
		}
		rows = append(rows, full)
	}
	rows = append(rows,
		"",
		s.Muted.Render("Enter cycles through eligible providers for the focused row."),
	)
	chipR := hit.NewRow(hitsIf(ctx, clickable), originX, originY+len(rows))
	chipR.Add("  ")
	placeChip(chipR, ptr, s, "config:act:cycle", chipAs(s, k.Enter, "next provider"), c.actClick(k.Enter))
	rows = append(rows, chipR.String())
	return strings.Join(rows, "\n")
}

func (c *configScreen) renderAPIKeys(ctx screenCtx, width, originX, originY int) string {
	s, k := ctx.styles, ctx.keys
	clickable := !c.inputActive()
	ptr := pointer{}
	if clickable {
		ptr = ctx.pointer()
	}
	rows := c.keyProviders()
	if len(rows) == 0 {
		return s.Muted.Render("no cloud providers registered")
	}
	out := []string{
		s.HeaderEm.Render("API keys for cloud providers"),
		"",
	}
	idW := 14
	notesW := max(14, width-idW-22)
	out = append(out, s.Muted.Render(fmt.Sprintf("   %-*s  %-8s  %s", idW, "PROVIDER", "KEY", "NOTES")))
	cur := c.cursors[secAPIKeys]
	for i, pr := range rows {
		attn := c.providerNeedsKey(pr)
		selected := c.rightFocus && i == cur
		// A missing key on the ACTIVE route is the action item ("⚑ add key");
		// a missing key on an unused provider is just informational ("△ missing"),
		// so the flag never cries wolf about a provider you aren't using.
		var keyCell string
		switch {
		case pr.HasKey:
			keyCell = s.Success.Render("✓ " + pr.KeySource)
		case !pr.RequiresKey:
			// Local provider (Parakeet) / fake — runs on-device, needs no key.
			keyCell = s.Success.Render("✓ local")
		case attn:
			keyCell = s.BadgeWarn.Render("⚑ add key")
		default:
			keyCell = s.Warning.Render("△ missing")
		}
		notes := pr.Notes
		if pr.IsActiveSpeech {
			notes = s.Success.Render("active speech · ") + notes
		} else if pr.IsActiveLLM {
			notes = s.Success.Render("active llm · ") + notes
		}
		idCell := s.HeaderEm.Render(fit(pr.ID, idW))
		body := fmt.Sprintf("%s  %-14s  %s", idCell, keyCell, fit(notes, notesW))
		// Same fixed 3-cell gutter as the dashboard/speaker rows: the amber bar
		// and the selection cursor COEXIST, so a flagged row keeps its bar even
		// when you move onto it. Full-row highlight is preserved for selection.
		if selected {
			body = s.RowSelected.Render(body)
		}
		full := attnGutter(s, selected, attn) + body
		if clickable {
			i := i
			id := fmt.Sprintf("config:key:%d", i)
			protect := 0
			if attn {
				protect = 1 // keep the amber ▍ attention bar beside the selection fill
			}
			full = rowFeedback(full, ptr.state(id, selected), s, width, protect)
			ctx.hits.Add(hit.Rect{X: originX, Y: originY + len(out), W: width, H: 1},
				region{id: id, onClick: func() tea.Cmd { c.focusContentRow(secAPIKeys, i); return nil }})
		}
		out = append(out, full)
	}
	out = append(out, "")
	// Action chips, each clickable: the click focuses the right pane (so the
	// key's handler runs) then replays the key. The chip text is unchanged from
	// the keyboard hints — the edit chip still advertises both ⏎ and e, and a
	// click replays e.
	chips := []struct {
		id      string
		text    string
		onClick clickAction
	}{
		{"config:act:edit", chipPair(s, k.Enter, k.Edit, "edit key"), c.actClick(k.Edit)},
		{"config:act:test", chipAs(s, k.Test, "test connectivity"), c.actClick(k.Test)},
		{"config:act:remove", chipAs(s, k.Remove, "remove key"), c.actClick(k.Remove)},
	}
	for _, ch := range chips {
		r := hit.NewRow(hitsIf(ctx, clickable), originX, originY+len(out))
		r.Add("  ")
		placeChip(r, ptr, s, ch.id, ch.text, ch.onClick)
		out = append(out, r.String())
	}
	return strings.Join(out, "\n")
}

func (c *configScreen) renderStorage(s theme.Styles, width int) string {
	_ = width
	if c.storageLoad {
		return s.Muted.Render("loading storage…")
	}
	row := func(label, value string) string {
		return s.Muted.Render(fmt.Sprintf("  %-18s", label)) + s.HeaderEm.Render(value)
	}
	indexCell := def(c.storage.IndexState, "clean")
	switch indexCell {
	case "clean":
		indexCell = s.Success.Render("✓ clean")
	case "indexing":
		indexCell = s.Info.Render("⟳ indexing")
	default:
		indexCell = s.Warning.Render("△ " + indexCell)
	}
	lines := []string{
		s.HeaderEm.Render("Local artifact health"),
		"",
		row("recordings dir", c.storage.RecordingsDir),
		s.Muted.Render(fmt.Sprintf("  %-18s", "meetings")) + s.HeaderEm.Render(fmt.Sprintf("%d", c.storage.MeetingCount)),
		s.Muted.Render(fmt.Sprintf("  %-18s", "index")) + indexCell,
		row("schema version", def(c.storage.SchemaVersion, "config.v1")),
		"",
		s.HeaderEm.Render("Retention"),
		"  " + s.Muted.Render("delete raw audio after valid transcript:") + "  " + s.Warning.Render("off"),
	}
	return strings.Join(lines, "\n")
}

func (c *configScreen) renderPaths(s theme.Styles, _ int) string {
	row := func(label, value string) string {
		return s.Muted.Render(fmt.Sprintf("  %-18s", label)) + s.HeaderEm.Render(value)
	}
	return strings.Join([]string{
		s.HeaderEm.Render("Filesystem"),
		"",
		row("config dir", c.cfg.ConfigDir),
		row("artifact root", c.cfg.ArtifactRoot),
		row("recordings dir", c.cfg.RecordingsDir),
		"",
		s.HeaderEm.Render("UI"),
		row("theme", c.cfg.UI.Theme),
	}, "\n")
}

// renderDeployment draws a selectable list of deployment shapes with the diagram
// for the selected shape BELOW it. Selection-first (like the rest of the config
// pane: controls first, rendered detail after) keeps the list fixed in place, so
// cycling to a taller diagram never shifts the rows you're navigating. With the
// right pane focused, ↑↓ cycles the preview and a click selects a row, so every
// shape — including the multi-machine ones — can be viewed even on a plain local
// install. It's a viewer: applying a shape is still done by editing
// compute/backend/storage in config.
//
// Every row is composed at the SAME geometry (badge · padded label · desc) and
// the selected/hover state is applied by rowFeedback on top, so a row never
// shifts its columns between focused and unfocused.
func (c *configScreen) renderDeployment(ctx screenCtx, width, originX, originY int) string {
	s := ctx.styles
	if c.sysLoad {
		return s.Muted.Render("loading deployment…")
	}
	clickable := !c.inputActive()
	ptr := pointer{}
	if clickable {
		ptr = ctx.pointer()
	}

	previews := c.topoPreviews()
	cur := layout.Clamp(c.cursors[secDeployment], 0, len(previews)-1)

	rows := []string{
		s.HeaderEm.Render("Deployment shape") +
			s.Muted.Render("   ↑↓ preview · edit config to apply"),
		"",
	}
	baseY := len(rows) // body lines before the first list row

	const labelW = 15
	for i, p := range previews {
		selected := c.rightFocus && i == cur
		badge := "  "
		if p.live {
			badge = s.Success.Render("● ")
		}
		content := badge + s.HeaderEm.Render(fmt.Sprintf("%-*s", labelW, p.label)) +
			"  " + s.Muted.Render(p.desc)
		line := clipLine(content, width)
		st := uiNormal
		if selected {
			st = uiActive
		}
		if clickable {
			i := i
			id := fmt.Sprintf("config:preset:%d", i)
			st = ptr.state(id, selected)
			ctx.hits.Add(hit.Rect{X: originX, Y: originY + baseY + i, W: width, H: 1},
				region{id: id, onClick: func() tea.Cmd {
					c.focusContentRow(secDeployment, i)
					return c.maybeStartAnim()
				}})
		}
		rows = append(rows, rowFeedback(line, st, s, width, 0))
	}

	// The diagram for the selected shape sits last, below the (fixed) list.
	sys, backend := previews[cur].sys, previews[cur].backendRemote
	rows = append(rows, "", renderTopology(sys, s, width, c.topoFrame, backend))
	return strings.Join(rows, "\n")
}

func (c *configScreen) renderKeyOverlay(s theme.Styles) string {
	if !c.keyEdit {
		return ""
	}
	return "\n\n" + strings.Join([]string{
		s.OverlayTitle.Render("Set API key for " + c.editID),
		c.keyInput.View(),
		s.Muted.Render("enter to save · esc to cancel"),
	}, "\n")
}
