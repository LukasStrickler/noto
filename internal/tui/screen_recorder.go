package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/lukasstrickler/noto/internal/notoapi"
	"github.com/lukasstrickler/noto/internal/tui/layout"
	"github.com/lukasstrickler/noto/internal/tui/theme"
)

// recorderScreen — start/stop a recording, watch live mic/system
// levels, and drop markers. The viz buffers the last ~120 meter
// samples so the waveform always has something to draw, even at
// dry-run rates.
type recorderScreen struct {
	rec        notoapi.RecordingState
	titleIn    textinput.Model
	titleFocus bool
	preflight  *notoapi.PreflightResult
	err        error

	micHist  []int
	partHist []int

	// Held peak (dBFS) per channel, decaying over samples, plus a latched
	// clip indicator — implements design.md's "peak holds then decays" and
	// "clipping turns the label into persistent CLIP" rules.
	micPeak, partPeak int
	micClip, partClip bool
}

const (
	waveBuffer = 120

	dbFloor          = -60 // quietest level the meter shows
	peakDecayPerTick = 1   // dB the held peak falls per incoming sample
	clipThresholdDB  = -1  // at/above this, latch CLIP
)

// updatePeak decays the previous held peak by one step, then raises it to
// the new sample if louder. Once a sample reaches the clip threshold the
// latch stays set (persistent CLIP) until the meter is reset.
func updatePeak(prev int, clipped bool, sample int) (int, bool) {
	peak := prev - peakDecayPerTick
	if peak < dbFloor {
		peak = dbFloor
	}
	if sample > peak {
		peak = sample
	}
	if sample >= clipThresholdDB {
		clipped = true
	}
	return peak, clipped
}

func newRecorderScreen() screen {
	ti := textinput.New()
	ti.Placeholder = "title — e.g. Roadmap sync"
	ti.CharLimit = 120
	return &recorderScreen{
		titleIn:  ti,
		micHist:  make([]int, 0, waveBuffer),
		partHist: make([]int, 0, waveBuffer),
		micPeak:  dbFloor,
		partPeak: dbFloor,
	}
}

func (r *recorderScreen) id() screenID      { return sRecorder }
func (r *recorderScreen) title() string     { return "recorder" }
func (r *recorderScreen) inputActive() bool { return r.titleFocus }

func (r *recorderScreen) enter(ctx screenCtx, _ string) tea.Cmd {
	return tea.Batch(fetchRecording(ctx), preflightCmd(ctx))
}
func (r *recorderScreen) leave(_ screenCtx) tea.Cmd { return nil }

func (r *recorderScreen) update(ctx screenCtx, msg tea.Msg) (screen, tea.Cmd) {
	switch v := msg.(type) {
	case recordingStateMsg:
		r.rec = v.State
	case recordingStartedMsg:
		if v.Err != nil {
			r.err = v.Err
			return r, func() tea.Msg { return bannerMsg{Kind: "error", Text: v.Err.Error()} }
		}
		r.micHist = r.micHist[:0]
		r.partHist = r.partHist[:0]
		r.micPeak, r.partPeak = dbFloor, dbFloor
		r.micClip, r.partClip = false, false
		return r, tea.Batch(
			fetchRecording(ctx),
			func() tea.Msg { return bannerMsg{Kind: "info", Text: "recording started"} },
		)
	case recordingStoppedMsg:
		if v.Err != nil {
			return r, func() tea.Msg { return bannerMsg{Kind: "error", Text: v.Err.Error()} }
		}
		jobs := ""
		if len(v.Res.JobsKicked) > 0 {
			jobs = fmt.Sprintf("  jobs: %s", strings.Join(v.Res.JobsKicked, ", "))
		}
		return r, tea.Batch(
			fetchRecording(ctx),
			func() tea.Msg { return bannerMsg{Kind: "info", Text: "recording stopped" + jobs} },
		)
	case preflightMsg:
		r.preflight = &v.Res
	case eventStreamMsg:
		switch {
		case v.Event.Kind == notoapi.EventRecorder && v.Event.Recorder != nil:
			r.rec = *v.Event.Recorder
		case v.Event.Kind == notoapi.EventMeter && v.Event.Meter != nil:
			r.pushSample(v.Event.Meter.MicDB, v.Event.Meter.ParticipantDB)
		}
	case tea.KeyMsg:
		if r.titleFocus {
			switch v.String() {
			case "esc", "enter":
				r.titleFocus = false
				r.titleIn.Blur()
				return r, nil
			}
			var cmd tea.Cmd
			r.titleIn, cmd = r.titleIn.Update(msg)
			return r, cmd
		}
		switch {
		case key.Matches(v, ctx.keys.EditTitle):
			r.titleFocus = true
			r.titleIn.Focus()
			return r, nil
		case key.Matches(v, ctx.keys.Record):
			if r.rec.Active {
				return r, nil
			}
			t := strings.TrimSpace(r.titleIn.Value())
			if t == "" {
				t = "Untitled meeting"
			}
			return r, startRecordingCmd(ctx, notoapi.StartRecordingOpts{
				Title:     t,
				Sources:   []string{"microphone", "system_audio"},
				AfterStop: notoapi.AfterStop{Ingest: true, Transcribe: true, Summarize: true, Index: true},
			})
		case key.Matches(v, ctx.keys.Stop):
			return r, stopRecordingCmd(ctx)
		case key.Matches(v, ctx.keys.Marker):
			return r, markerCmd(ctx, "marker")
		}
	}
	return r, nil
}

func (r *recorderScreen) pushSample(mic, part int) {
	r.micHist = append(r.micHist, mic)
	if len(r.micHist) > waveBuffer {
		r.micHist = r.micHist[len(r.micHist)-waveBuffer:]
	}
	r.partHist = append(r.partHist, part)
	if len(r.partHist) > waveBuffer {
		r.partHist = r.partHist[len(r.partHist)-waveBuffer:]
	}
	r.micPeak, r.micClip = updatePeak(r.micPeak, r.micClip, mic)
	r.partPeak, r.partClip = updatePeak(r.partPeak, r.partClip, part)
}

func (r *recorderScreen) view(ctx screenCtx) string {
	s := ctx.styles
	var body string
	subtitle := ""
	if r.rec.Active {
		body = r.renderActive(ctx)
		subtitle = "recording — press " + ctx.keys.Stop.Help().Key + " to stop"
	} else {
		body = r.renderIdle(ctx)
		subtitle = "press " + ctx.keys.Record.Help().Key + " to start"
	}
	return layout.Panel{
		Title:    "recorder",
		Subtitle: subtitle,
		Width:    ctx.width,
		Height:   ctx.height,
		Focused:  true,
		Body:     body,
	}.Render(s)
}

func (r *recorderScreen) renderIdle(ctx screenCtx) string {
	s := ctx.styles
	titleLine := r.titleIn.View()
	if !r.titleFocus && strings.TrimSpace(r.titleIn.Value()) == "" {
		titleLine = s.Muted.Render(r.titleIn.Placeholder)
	}

	pre := s.Muted.Render("checking permissions…")
	if r.preflight != nil {
		switch {
		case r.preflight.MicReady && r.preflight.SystemReady:
			pre = s.Success.Render("✓ microphone + system audio ready")
		case r.preflight.MicReady:
			pre = s.Warning.Render("△ mic ok · system audio unavailable")
		default:
			pre = s.Warning.Render("△ " + r.preflight.Diagnostic)
		}
	}

	headline := s.HeaderEm.Render("Start a recording")
	field := s.Muted.Render("title  ") + titleLine
	sources := s.Muted.Render("sources  ") + s.Secondary.Render("microphone + system audio")
	after := s.Muted.Render("pipeline ") + s.Secondary.Render("ingest → transcribe → summarize → index")

	chips := strings.Join([]string{
		chipAs(s, ctx.keys.EditTitle, "edit title"),
		chipAs(s, ctx.keys.Record, "start"),
		chip(s, ctx.keys.Palette),
		chip(s, ctx.keys.Back),
	}, "   ")

	return strings.Join([]string{
		headline,
		"",
		field,
		sources,
		after,
		"",
		pre,
		"",
		s.HintBar.Render(chips),
	}, "\n")
}

func (r *recorderScreen) renderActive(ctx screenCtx) string {
	s := ctx.styles
	rec := r.rec
	width := ctx.width - 6
	if width < 30 {
		width = 30
	}

	timer := s.Recording.Render("● REC") + "  " + s.HeaderEm.Render(formatDuration(rec.ElapsedSec))
	title := s.HeaderEm.Render(def(rec.Title, "Untitled meeting"))
	sources := s.Muted.Render(strings.Join(rec.Sources, " · "))

	waveW := width - 14
	if waveW < 20 {
		waveW = 20
	}
	wave := strings.Join([]string{
		renderWaveLane(s, "me/mic ", rec.MicDB, r.micPeak, r.micClip, r.micHist, waveW, 0),
		renderWaveLane(s, "system ", rec.ParticipantDB, r.partPeak, r.partClip, r.partHist, waveW, 1),
	}, "\n")

	chips := strings.Join([]string{
		chip(s, ctx.keys.Stop),
		chip(s, ctx.keys.Marker),
		chip(s, ctx.keys.Palette),
	}, "   ")

	markers := ""
	if len(rec.Markers) > 0 {
		var lines []string
		lines = append(lines, s.PanelTitle.Render("markers"))
		for _, m := range rec.Markers {
			lines = append(lines, "  "+s.Muted.Render(formatSec(float64(m.OffsetSec)))+"  "+m.Label)
		}
		markers = "\n" + strings.Join(lines, "\n")
	}

	return strings.Join([]string{
		timer,
		title,
		sources,
		"",
		wave,
		markers,
		"",
		s.HintBar.Render(chips),
	}, "\n")
}

// renderWaveLane draws one label + bar-pixel waveform + live dB read-out.
// The read-out also shows the held peak (peak-hold/decay) and latches a
// persistent CLIP marker once a channel has clipped. channelIdx routes
// me/mic to Primary and system to Secondary.
func renderWaveLane(s theme.Styles, label string, current, peak int, clip bool, hist []int, w, channelIdx int) string {
	if w < 8 {
		w = 8
	}
	// Each cell is one of 8 vertical glyphs.
	const glyphs = " ▁▂▃▄▅▆▇█"
	// Sample the last w samples from hist; pad with silence.
	bars := make([]rune, w)
	start := len(hist) - w
	if start < 0 {
		start = 0
	}
	pad := w - (len(hist) - start)
	for i := 0; i < pad; i++ {
		bars[i] = ' '
	}
	for i, v := range hist[start:] {
		// Map -60..0 dBFS to 0..8 glyph buckets.
		bucket := int(float64(v+60) / 60.0 * 8)
		if bucket < 0 {
			bucket = 0
		}
		if bucket > 8 {
			bucket = 8
		}
		bars[pad+i] = rune(glyphs[bucket])
	}

	var color lipgloss.Color
	switch channelIdx {
	case 0:
		color = s.T.Primary
	case 1:
		color = s.T.Secondary
	default:
		color = s.T.Info
	}
	if current >= -6 {
		color = s.T.Danger
	}

	bar := lipgloss.NewStyle().Foreground(color).Render(string(bars))
	level := s.Muted.Render(fmt.Sprintf("%+3d dB", current))
	peakStr := s.Muted.Render(fmt.Sprintf("peak %+3d", peak))
	readout := level + "  " + peakStr
	if clip {
		readout += "  " + s.Recording.Render("CLIP")
	}
	return s.Muted.Render(label) + bar + "  " + readout
}

type preflightMsg struct {
	Res notoapi.PreflightResult
	Err error
}

func preflightCmd(ctx screenCtx) tea.Cmd {
	return func() tea.Msg {
		res, err := ctx.client.PreflightRecording(ctx.ctx)
		return preflightMsg{Res: res, Err: err}
	}
}
