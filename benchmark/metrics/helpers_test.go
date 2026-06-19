package metrics

import (
	"math"
	"testing"

	// Register the suite's -hours/-seed flags so `go test ./benchmark/... -hours=N`
	// parses here too (no-op for these pure scorers — there's no audio to subsample).
	_ "github.com/lukasstrickler/noto/benchmark/internal/sample"
)

func approx(t *testing.T, name string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("%s = %v, want %v", name, got, want)
	}
}
