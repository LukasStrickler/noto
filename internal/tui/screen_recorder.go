package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/lukasstrickler/noto/internal/notoapi"
	"github.com/lukasstrickler/noto/internal/tui/keys"
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
}

const waveBuffer = 120

func newRecorderScreen() screen {
	ti := textinput.New()
	ti.Placeholder = "title — e.g. Roadmap sync"
	ti.CharLimit = 120
	return &recorderScreen{
		titleIn:  ti,
		micHist:  make([]int, 0, waveBuffer),
		partHist: make([]int, 0, waveBuffer),
	}
}

func (r *recorderScreen) id() screenID         { return sRecorder }
func (r *recorderScreen) title() string        { return "recorder" }
func (r *recorderScreen) helpKeys() []keys.Map { return nil }
func (r *recorderScreen) inputActive() bool    { return r.titleFocus }

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
		case v.String() == "i":
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
}

func (r *recorderScreen) view(ctx screenCtx) string {
	s := ctx.styles
	var body string
	subtitle := ""
	if r.rec.Active {
		body = r.renderActive(ctx)
		subtitle = "recording — press s to stop"
	} else {
		body = r.renderIdle(ctx)
		subtitle = "press r to start"
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
	width := ctx.width - 6
	if width < 30 {
		width = 30
	}

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
		hint(s, "i", "edit title"),
		hint(s, "r", "start"),
		hint(s, ":", "menu"),
		hint(s, "esc", "back"),
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
		renderWaveLane(s, "me/mic ", rec.MicDB, r.micHist, waveW, 0),
		renderWaveLane(s, "system ", rec.ParticipantDB, r.partHist, waveW, 1),
	}, "\n")

	chips := strings.Join([]string{
		hint(s, "s", "stop"),
		hint(s, "n", "marker"),
		hint(s, ":", "menu"),
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
// channelIdx routes me/mic to Primary and system to Secondary.
func renderWaveLane(s theme.Styles, label string, current int, hist []int, w, channelIdx int) string {
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
	return s.Muted.Render(label) + bar + "  " + level
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
