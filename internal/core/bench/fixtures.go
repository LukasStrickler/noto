// Package bench holds pure benchmark compare, adoption, and signature logic.
package bench

import (
	"path/filepath"
	"runtime"
)

// FixtureRoot is testdata/bench at the module root.
func FixtureRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "..", "testdata", "bench")
}
