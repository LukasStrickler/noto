// Package bench holds pure benchmark compare, adoption, and signature logic.
package bench

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// FixtureRoot is testdata/bench at the module root.
func FixtureRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "..", "testdata", "bench")
}

func LoadFixture(name string) ([]byte, error) {
	return os.ReadFile(filepath.Join(FixtureRoot(), name))
}

func loadFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := LoadFixture(name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
