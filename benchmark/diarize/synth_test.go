package diarize

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/lukasstrickler/noto/benchmark/dataset"
	"github.com/lukasstrickler/noto/benchmark/internal/bench"
	"github.com/lukasstrickler/noto/benchmark/metrics"
	diar "github.com/lukasstrickler/noto/internal/platform/providers/diarize"
)

func TestDiarizeSynthetic(t *testing.T) {
	dir := os.Getenv("BENCH_SYNTH_DIR")
	if dir == "" {
		t.Skip("set BENCH_SYNTH_DIR to the synthetic_meetings corpus")
	}
	d, ok := buildDiarizer(t)
	if !ok {
		t.Skip("no diarizer engine configured (set BENCH_DIAR_ENGINE to a registered engine)")
	}
	meetings := syntheticMeetings(t, dir)
	if len(meetings) == 0 {
		t.Skip("no synthetic meetings")
	}

	type diarRes struct {
		derTotal, refSpeech, attr float64
		wall                      time.Duration
		audio                     float64
	}
	t.Logf("%-8s %8s %8s %8s %8s %7s", "meeting", "DER", "miss", "FA", "conf", "RTF")
	results := bench.RunMeetings(t, meetings, func(t *testing.T, m dataset.Meeting) diarRes {
		audio, err := os.ReadFile(m.AudioPath)
		if err != nil {
			t.Fatalf("read %s: %v", m.AudioPath, err)
		}
		start := time.Now()
		turns, err := d.Diarize(context.Background(), audio, diar.DiarizeOptions{MeetingID: m.ID})
		wall := time.Since(start)
		if err != nil {
			t.Fatalf("Diarize %s: %v", m.ID, err)
		}
		ref := m.Segments()
		s := score(ref, turns, wall, meetingDuration(ref))
		t.Logf("%-8s %7.1f%% %7.1f%% %7.1f%% %7.1f%% %7.2f",
			m.ID, s.DER.Rate*100, pct(s.DER.Missed, s.DER.RefSpeech), pct(s.DER.FalseAlarm, s.DER.RefSpeech),
			pct(s.DER.Confusion, s.DER.RefSpeech), s.RTF)
		return diarRes{derTotal: s.DER.Total(), refSpeech: s.DER.RefSpeech, attr: s.Attribution, wall: wall, audio: meetingDuration(ref)}
	})

	var sumErr, sumRef, sumAttr float64
	var sumWall time.Duration
	var sumAudio float64
	for _, r := range results {
		sumErr += r.derTotal
		sumRef += r.refSpeech
		sumAttr += r.attr
		sumWall += r.wall
		sumAudio += r.audio
	}
	overall := 0.0
	if sumRef > 0 {
		overall = sumErr / sumRef
	}
	t.Logf("OVERALL SYNTH DER %.1f%% over %.0fs ref speech; mean attribution %.1f%%; RTF %.2f",
		overall*100, sumRef, sumAttr/float64(len(meetings))*100,
		metrics.Timing{Stage: "diarize", Wall: sumWall, AudioSeconds: sumAudio}.RTF())
}

func syntheticMeetings(t *testing.T, dir string) []dataset.Meeting {
	t.Helper()
	rttms, _ := filepath.Glob(filepath.Join(dir, "*.rttm"))
	var meetings []dataset.Meeting
	for _, r := range rttms {
		id := trimExt(filepath.Base(r))
		wav := filepath.Join(dir, id+".wav")
		if !fileExists(wav) {
			continue
		}
		turns, err := dataset.LoadRTTM(r)
		if err != nil {
			t.Fatalf("LoadRTTM %s: %v", r, err)
		}
		meetings = append(meetings, dataset.Meeting{ID: id, AudioPath: wav, Turns: turns})
	}
	sort.Slice(meetings, func(i, j int) bool { return meetings[i].ID < meetings[j].ID })
	return bench.Select(t, meetings)
}
