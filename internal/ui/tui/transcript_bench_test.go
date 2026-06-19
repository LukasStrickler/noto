package tui

import (
	"context"
	"fmt"
	"testing"

	"github.com/lukasstrickler/noto/internal/transport/notoapi"
	"github.com/lukasstrickler/noto/internal/ui/tui/keys"
	"github.com/lukasstrickler/noto/internal/ui/tui/theme"
)

// benchTranscriptPane builds a detailPane with an N-segment transcript and a
// handful of speakers, mirroring a real long meeting on the transcript tab.
func benchTranscriptPane(nSegs, nSpeakers int) *detailPane {
	d := newDetailPane()
	d.id_ = "m1"
	d.tab = tabTranscript

	speakers := make([]notoapi.Speaker, nSpeakers)
	stats := make([]speakerStat, nSpeakers)
	for i := 0; i < nSpeakers; i++ {
		id := fmt.Sprintf("spk_%d", i)
		label := fmt.Sprintf("Speaker %c", 'A'+i)
		speakers[i] = notoapi.Speaker{ID: id, Label: label, DisplayName: label}
		stats[i] = speakerStat{ID: id, Name: label, token: label, colorIdx: i % 6, TalkSec: 100}
	}
	d.speakers = stats

	segs := make([]notoapi.TranscriptSegment, nSegs)
	sentence := "We discussed the rollout plan and the owner agreed to follow up with the team next week about the remaining open questions."
	for i := 0; i < nSegs; i++ {
		segs[i] = notoapi.TranscriptSegment{
			ID:        fmt.Sprintf("seg_%03d", i),
			SpeakerID: speakers[i%nSpeakers].ID,
			StartSec:  float64(i * 6),
			EndSec:    float64(i*6 + 6),
			Text:      sentence,
		}
	}
	d.transcript = notoapi.Transcript{MeetingID: "m1", Segments: segs, Speakers: speakers}
	return d
}

func benchTranscriptCtx() screenCtx {
	return screenCtx{ctx: context.Background(), keys: keys.New(), styles: theme.NewStyles(), width: 100, height: 40}
}

// BenchmarkRenderTranscript measures one transcript-tab body render — the work
// repeated every frame while the user scrolls. This is the steady-state cost of
// an arrow-key/wheel scroll notch, which dirties the frame and rebuilds.
func BenchmarkRenderTranscript(b *testing.B) {
	for _, n := range []int{100, 500} {
		b.Run(fmt.Sprintf("segs=%d", n), func(b *testing.B) {
			d := benchTranscriptPane(n, 4)
			ctx := benchTranscriptCtx()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = d.renderTranscript(ctx, 80, 40)
			}
		})
	}
}
