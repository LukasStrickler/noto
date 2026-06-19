package bench

import (
	"fmt"
	"os"
	"path/filepath"
)

// RepoRoot finds the module root (directory containing go.mod).
func RepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found from cwd")
		}
		dir = parent
	}
}

// venvPython returns the dedicated .venv-modal interpreter path and whether it
// exists. The bench shells out to this interpreter (it carries modal + torch +
// nemo + pyannote); a missing one is the difference between a real Modal run and
// a cryptic ImportError, so the readiness gate keys off the same probe.
func venvPython(repoRoot string) (string, bool) {
	for _, c := range []string{
		filepath.Join(repoRoot, ".venv-modal", "bin", "python"),
		filepath.Join(repoRoot, ".venv-modal", "bin", "python3"),
	} {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c, true
		}
	}
	return "", false
}

// PythonForBench resolves the Modal benchmark interpreter, falling back to the
// system python3 when the dedicated venv is absent (callers that require the venv
// gate on CheckModalPrereqs first).
func PythonForBench(repoRoot string) string {
	if p, ok := venvPython(repoRoot); ok {
		return p
	}
	return "python3"
}
