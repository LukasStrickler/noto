package tui

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/lukasstrickler/noto/internal/transport/notoapi"
	"github.com/lukasstrickler/noto/internal/ui/tui/hit"
	"github.com/lukasstrickler/noto/internal/ui/tui/keys"
	"github.com/lukasstrickler/noto/internal/ui/tui/theme"
)

// BenchmarkDashboardList measures one meeting-list render. rowList renders only
// the rows in the viewport, so the cost is the window — a long library should
// cost about the same per frame as a short one (what a scroll notch repeats).
func BenchmarkDashboardList(b *testing.B) {
	for _, n := range []int{50, 1000} {
		b.Run(fmt.Sprintf("meetings=%d", n), func(b *testing.B) {
			m := newDashboardScreen().(*dashboardScreen)
			m.loading = false
			base := time.Date(2026, 5, 20, 9, 0, 0, 0, time.UTC)
			m.all = make([]notoapi.Meeting, n)
			for i := range m.all {
				m.all[i] = notoapi.Meeting{
					ID: fmt.Sprintf("m%d", i), Title: fmt.Sprintf("Meeting number %d about the thing", i),
					Status: notoapi.StatusSummarized, CreatedAt: base, DecisionCount: 2, ActionCount: 1,
				}
			}
			ctx := screenCtx{ctx: context.Background(), keys: keys.New(), styles: theme.NewStyles(), width: 140, height: 30, hits: &hit.Map[region]{}}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				ctx.hits = &hit.Map[region]{}
				_ = m.renderList(ctx, 60, 26, 0, 0)
			}
		})
	}
}
