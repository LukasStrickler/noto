// Package bench is shared test-support for the benchmark suite. It gives every
// bench the same ergonomics: standard run flags (-hours, -seed), reproducible
// sample selection, and run recording + comparison so you can see whether the
// system is improving or degrading over time.
//
// It is imported only by the benchmark test binaries (never by production), so
// the flags it registers and its dependency on `testing` stay out of any shipped
// binary.
//
// Typical use in a bench:
//
//	meetings := bench.Select(t, allMeetings)   // honors -hours / -seed, logs the pick
//	... run + score ...
//	bench.Track(t, "e2e", current)             // record the run + print the trend
package bench

import (
	"sort"
	"sync"
	"testing"

	"github.com/lukasstrickler/noto/benchmark/dataset"
	"github.com/lukasstrickler/noto/benchmark/internal/sample"
)

// DefaultSeed is re-exported from the sample package (which owns the flags) so
// callers and tests keep referring to bench.DefaultSeed.
const DefaultSeed = sample.DefaultSeed

// RunMeetings runs fn for every meeting and returns the per-meeting results in
// input order, ready to aggregate. With -jobs=1 (the default, "prod") meetings
// run one at a time — the honest single-stream timing a real user sees. With
// -jobs>1 ("benchmark") up to that many run concurrently. Concurrency NEVER
// changes the scores: each meeting is transcribed and scored on its own, fn
// writes only its own result slot (race-free), and the caller aggregates after
// every subtest has finished. Each meeting is its own subtest, so fn may call
// t.Fatalf without taking down its siblings.
//
// Scheduling is a WORK-STEALING pool, not a lock-step wave: `jobs` workers each
// pull the next meeting from a shared queue the instant they free up, so a worker
// finishing a short meeting immediately starts another instead of all streams
// reaching their (GPU-idle) CPU clustering tail together. The queue is ordered
// LONGEST-FIRST (LPT list-scheduling): a long meeting admitted last would strand
// the other workers idle at the tail (a synchronized GPU gap), so we start the
// long ones first and let the short ones backfill — this minimizes the makespan
// and keeps the card fed to the end. Results still land in INPUT order.
func RunMeetings[T any](t *testing.T, meetings []dataset.Meeting, fn func(t *testing.T, m dataset.Meeting) T) []T {
	t.Helper()
	out := make([]T, len(meetings))
	jobs := *sample.Jobs
	if jobs < 1 {
		jobs = 1
	}
	if jobs > len(meetings) {
		jobs = len(meetings)
	}

	// LPT order: indices into `meetings`, longest audio first. Stable so equal-
	// length meetings keep input order (reproducible). out[idx] preserves input
	// order regardless of the order we process them in.
	order := make([]int, len(meetings))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		return meetings[order[a]].AudioSeconds() > meetings[order[b]].AudioSeconds()
	})
	queue := make(chan int, len(order))
	for _, idx := range order {
		queue <- idx
	}
	close(queue)

	// The inner "meetings" group blocks until every worker drains the queue, so
	// `out` is fully populated when RunMeetings returns. Concurrent t.Run is
	// allowed (each call returns before this parent does); the subtests run
	// SYNCHRONOUSLY inside their worker (no t.Parallel), so the only concurrency is
	// the `jobs` workers — exactly the bound we want, with deterministic LPT pull
	// order instead of a semaphore race.
	t.Run("meetings", func(t *testing.T) {
		var wg sync.WaitGroup
		for w := 0; w < jobs; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for idx := range queue {
					idx, m := idx, meetings[idx]
					t.Run(m.ID, func(t *testing.T) {
						out[idx] = fn(t, m)
					})
				}
			}()
		}
		wg.Wait()
	})
	return out
}

// Select returns the meetings to run: the whole corpus by default, or a
// reproducible ~`-hours` subset chosen with `-seed`. It always yields at least
// one WHOLE meeting and logs exactly what it picked so a run is self-describing.
func Select(t testing.TB, meetings []dataset.Meeting) []dataset.Meeting {
	t.Helper()
	hours, seed := *sample.Hours, *sample.Seed
	sub := dataset.SelectHours(meetings, hours, seed)
	if hours > 0 {
		t.Logf("sample: -hours=%g -seed=%d → %d/%d meetings (~%.2f h audio)",
			hours, seed, len(sub), len(meetings), dataset.TotalHours(sub))
	}
	return sub
}

// Track records a run to the history at `path` and logs a trend table against
// the previous run with the same label — the "are we improving or degrading?"
// view. It is best-effort: a logging failure never fails the test.
func Track(t testing.TB, path, label, runtime string, metrics map[string]float64) {
	t.Helper()
	prev, err := AppendRun(path, NewRun(label, runtime, metrics))
	if err != nil {
		t.Logf("run history unavailable (%v)", err)
		return
	}
	if prev == nil {
		t.Logf("trend[%s]: first recorded run — nothing to compare yet", label)
		return
	}
	t.Logf("trend[%s] vs %s:", label, prev.Time)
	t.Logf("  %-12s %10s %10s %8s", "metric", "prev", "current", "Δ")
	for _, c := range Compare(prev, metrics2run(metrics)) {
		t.Logf("  %-12s %10.3f %10.3f %+8.3f %s", c.Name, c.Previous, c.Current, c.Delta, c.Arrow())
	}
}

func metrics2run(m map[string]float64) Run { return Run{Metrics: m} }
