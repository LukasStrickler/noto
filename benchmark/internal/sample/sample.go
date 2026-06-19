// Package sample owns the benchmark suite's run-subsampling flags (-hours, -seed)
// and nothing else. It imports only the standard library, so ANY benchmark test
// package can link it without an import cycle — which is the whole point.
//
// Go links a separate test binary per package and runs each with the SAME flag
// set, so `go test ./benchmark/... -hours=1` fails on any package whose binary
// doesn't define -hours. The asset benches (stt, diarize, e2e) get the flags
// transitively through the `bench` helper; the pure packages (metrics, dataset,
// identity) can't import `bench` (it imports them → cycle), so they blank-import
// THIS package instead:
//
//	import _ "github.com/lukasstrickler/noto/benchmark/internal/sample"
//
// That registers the flags so the shared invocation parses everywhere; -hours is
// simply a no-op where there's no audio to subsample. The rule is uniform: every
// benchmark test binary links the suite's flags, so the same command runs the
// same set of tests with or without a cap.
package sample

import "flag"

// DefaultSeed keeps the default sample reproducible: with no -seed, every run
// draws the SAME subset, so numbers are comparable out of the box. Change -seed
// to draw a different (still reproducible) spread.
const DefaultSeed = 1

var (
	// Hours caps a run to ~N hours of audio (whole meetings; 0 = the whole corpus).
	Hours = flag.Float64("hours", 0, "limit the run to ~N hours of audio (whole meetings; 0 = all)")
	// Seed makes the -hours sample deterministic and reproducible.
	Seed = flag.Int64("seed", DefaultSeed, "seed for reproducible -hours sample selection")
	// Jobs is how many meetings a bench processes concurrently. 1 (the default)
	// is a single stream — what a real user gets, and the honest measure of
	// per-meeting latency/RTF (the "prod" profile). >1 overlaps independent
	// meetings to saturate a benchmark GPU (the "fast" profile); it never changes
	// the scores, only wall time, because each meeting is scored independently.
	Jobs = flag.Int("jobs", 1, "concurrent meetings per bench (1 = prod single-stream; >1 = benchmark parallel)")
)
